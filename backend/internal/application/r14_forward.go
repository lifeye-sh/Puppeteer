package application

import (
	"context"
	"errors"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"net/netip"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Forwarding is a separate connection mode. It cannot share a terminal session.
// Targets are reached from this backend, never through the asset's SSH/Agent route.
func (a *App) nativeForwardLoop(l *liveSession, v session.Session, auth string, first ssh.NewChannel, channels <-chan ssh.NewChannel) {
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	defer func() { l.cancel(); wg.Wait() }()
	handle := func(ch ssh.NewChannel) {
		if ch.ChannelType() != "direct-tcpip" {
			_ = ch.Reject(ssh.Prohibited, "Forward-only connection")
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = ch.Reject(ssh.ResourceShortage, "Forward limit reached")
			return
		}
		wg.Add(1)
		go func() { defer wg.Done(); defer func() { <-slots }(); a.nativeForwardStream(l, v, auth, ch) }()
	}
	handle(first)
	for {
		select {
		case <-l.ctx.Done():
			return
		case ch, ok := <-channels:
			if !ok {
				return
			}
			handle(ch)
		}
	}
}

func (a *App) checkForward(ctx context.Context, v session.Session, auth, host string, port int, version int64) (nativeForwardRule, int64, error) {
	var rule nativeForwardRule
	var current int64
	e := a.db.Within(ctx, func(ctx context.Context) error {
		u, e := a.nativeSessionUser(ctx, v, auth)
		if e != nil {
			return e
		}
		if _, e = a.checkLiveUser(ctx, v, u); e != nil {
			return e
		}
		if v.Readonly {
			return fault.Denied
		}
		// Opaque traffic cannot satisfy command filters or read-only/file restrictions.
		for _, action := range []string{"execute", "upload", "download"} {
			d, e := a.policyDecision(ctx, u, v.AssetID, v.ClientIP, action, "")
			if e != nil {
				return e
			}
			if !d.Allowed || d.Filtered {
				return fault.Denied
			}
		}
		settings, e := a.forwardSettings(ctx)
		if e != nil {
			return e
		}
		if !settings.Enabled || (version != 0 && settings.Version != version) {
			return fault.Denied
		}
		current = settings.Version
		for _, r := range settings.Rules {
			if r.AssetID == v.AssetID && r.Host == host && r.Port == port {
				rule = r
				return nil
			}
		}
		return fault.Denied
	})
	return rule, current, e
}

func (a *App) nativeForwardStream(l *liveSession, v session.Session, auth string, in ssh.NewChannel) {
	if !a.allowAttempt("native-forward:" + v.ActorID) {
		_ = in.Reject(ssh.ResourceShortage, "Forward rate limit")
		return
	}
	var p struct {
		Host       string
		Port       uint32
		Origin     string
		OriginPort uint32
	}
	if len(in.ExtraData()) > 1024 || ssh.Unmarshal(in.ExtraData(), &p) != nil || !nativeLaunchHost(p.Host) || p.Port == 0 || p.Port > 65535 {
		_ = in.Reject(ssh.Prohibited, "Invalid destination")
		return
	}
	host := strings.ToLower(p.Host)
	if ip, e := netip.ParseAddr(host); e == nil {
		host = ip.Unmap().String()
	}
	ctx, cancel := context.WithCancel(l.ctx)
	defer cancel()
	check, stop := context.WithTimeout(ctx, 2*time.Second)
	rule, version, e := a.checkForward(check, v, auth, host, int(p.Port), 0)
	stop()
	if e != nil {
		_ = in.Reject(ssh.Prohibited, "Destination or policy denied")
		c, end := context.WithTimeout(context.Background(), 2*time.Second)
		defer end()
		if a.audit.Operation(c, v.ActorID, "native.forward_denied", "failed", v.ID, v.ClientIP) != nil {
			l.stop("forward_audit_failed")
		}
		return
	}
	var id string
	// Audit before dialing. The client-supplied origin is deliberately not trusted.
	e = a.db.Within(ctx, func(ctx context.Context) error {
		if e := a.db.Row(ctx, `INSERT INTO native_forward_streams(session_id,host,port,address) VALUES($1,$2,$3,'') RETURNING id`, v.ID, host, int(p.Port)).Scan(&id); e != nil {
			return e
		}
		return a.audit.Detail(ctx, v.ActorID, "native.forward_started", v.ID, v.ClientIP, map[string]string{"streamId": id, "host": host, "port": strconv.Itoa(int(p.Port)), "assetId": v.AssetID})
	})
	if e != nil {
		_ = in.Reject(ssh.ConnectionFailed, "Audit unavailable")
		l.stop("forward_audit_failed")
		return
	}
	state, address := "failed", ""
	var sent, received int64
	defer func() {
		c, end := context.WithTimeout(context.Background(), 2*time.Second)
		defer end()
		err := a.db.Within(c, func(c context.Context) error {
			if _, e := a.db.Exec(c, `UPDATE native_forward_streams SET state=$2,address=$3,bytes_to_target=$4,bytes_from_target=$5,ended_at=now() WHERE id=$1`, id, state, address, sent, received); e != nil {
				return e
			}
			return a.audit.Detail(c, v.ActorID, "native.forward_finished", v.ID, v.ClientIP, map[string]string{"streamId": id, "host": host, "port": strconv.Itoa(int(p.Port)), "address": address, "state": state, "bytesToTarget": strconv.FormatInt(sent, 10), "bytesFromTarget": strconv.FormatInt(received, 10)})
		})
		if err != nil {
			l.stop("forward_audit_failed")
		}
	}()
	dialCtx, dialStop := context.WithTimeout(ctx, 5*time.Second)
	defer dialStop()
	var remote net.Conn
	for _, ip := range rule.Addresses {
		if !forwardAddress(ip) {
			continue
		}
		remote, e = (&net.Dialer{}).DialContext(dialCtx, "tcp", net.JoinHostPort(ip, strconv.Itoa(int(p.Port))))
		if e == nil {
			address = ip
			break
		}
	}
	if remote == nil {
		_ = in.Reject(ssh.ConnectionFailed, "Target unavailable")
		return
	}
	defer remote.Close()
	closeRemote := context.AfterFunc(ctx, func() { _ = remote.Close() })
	defer closeRemote()
	check, stop = context.WithTimeout(ctx, 2*time.Second)
	_, _, e = a.checkForward(check, v, auth, host, int(p.Port), version)
	stop()
	if e != nil {
		_ = in.Reject(ssh.Prohibited, "Authorization changed")
		return
	}
	e = a.db.Within(ctx, func(ctx context.Context) error {
		// Multiple forwards can become ready simultaneously. Transition only once.
		if _, e := a.db.Exec(ctx, `UPDATE native_forward_streams SET state='connected',address=$2 WHERE id=$1`, id, address); e != nil {
			return e
		}
		tag, e := a.db.Exec(ctx, `UPDATE sessions SET state='connected',connected_at=now() WHERE id=$1 AND state='connecting' AND NOT terminate_requested AND lease_expires_at>now()`, v.ID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 1 {
			return a.audit.AccessState(ctx, v.ID, "connected", "", false)
		}
		return nil
	})
	if e != nil {
		_ = in.Reject(ssh.ConnectionFailed, "Audit unavailable")
		l.stop("forward_audit_failed")
		return
	}
	ch, requests, e := in.Accept()
	if e != nil {
		return
	}
	defer ch.Close()
	go ssh.DiscardRequests(requests)
	closeChannel := context.AfterFunc(ctx, func() { _ = ch.Close() })
	defer closeChannel()
	leaseDone := make(chan struct{})
	go func() {
		defer close(leaseDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c, end := context.WithTimeout(ctx, time.Second)
				_, _, e := a.checkForward(c, v, auth, host, int(p.Port), version)
				end()
				if e != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-leaseDone }()
	// Honor TCP/SSH EOF in each direction; bounded writes prevent a slow peer
	// from holding a stream forever. The session lease bounds idle lifetime.
	result := make(chan error, 1)
	go func() {
		var err error
		sent, err = io.Copy(&forwardWriter{remote, func() error { return a.sessionActivity(l, v.ID) }}, ch)
		if tcp, ok := remote.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		result <- err
	}()
	received, e = io.Copy(&forwardChannelWriter{Channel: ch, stop: l.cancel}, remote)
	_ = ch.CloseWrite()
	if e != nil {
		cancel()
	}
	var sendErr error
	select {
	case sendErr = <-result:
	case <-ctx.Done():
		_ = ch.Close()
		_ = remote.Close()
		sendErr = <-result
	}
	if ctx.Err() != nil {
		state = "interrupted"
	} else if e == nil && sendErr == nil {
		state = "closed"
	} else if errors.Is(e, net.ErrClosed) {
		state = "interrupted"
	}
}

type forwardWriter struct {
	net.Conn
	activity func() error
}

type forwardChannelWriter struct {
	ssh.Channel
	stop context.CancelFunc
}

func (w *forwardChannelWriter) Write(p []byte) (int, error) {
	// A peer withholding SSH window updates need not acknowledge Channel.Close.
	// Cancel the transport context so blocked writes cannot retain the lease.
	timer := time.AfterFunc(15*time.Second, w.stop)
	defer timer.Stop()
	return w.Channel.Write(p)
}

func (w *forwardWriter) Write(p []byte) (int, error) {
	if len(p) > 0 && w.activity != nil {
		if e := w.activity(); e != nil {
			return 0, e
		}
	}
	if e := w.SetWriteDeadline(time.Now().Add(15 * time.Second)); e != nil {
		return 0, e
	}
	return w.Conn.Write(p)
}
