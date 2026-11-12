package application

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	ws "github.com/coder/websocket"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/secretbox"
	"strings"
	"time"
)

// All SSH bytes travel inside an authenticated, one-use TLS bridge. This is not
// a public unauthenticated SSH listener. It terminates SSH here; target credentials
// stay on the server. Forwarding requires a separately reserved, policy-bound mode.
func (a *App) nativeBridge(w http.ResponseWriter, r *http.Request) error {
	ticket, e := nativeBearer(r)
	if e != nil {
		return e
	}
	if r.Header.Get("Sec-WebSocket-Protocol") != "nt-bridge.v1" || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return fault.Invalid
	}
	var v session.Session
	var family, id string
	var sealed []byte
	var clientKey string
	// Resolve before locking the user, then atomically burn the ticket. Target
	// dial, SSH handshake and even HTTP upgrade failures cannot restore it.
	e = a.db.Row(r.Context(), `SELECT session_id,family_id FROM native_bridge_reservations WHERE ticket_hash=$1 AND consumed_at IS NULL AND expires_at>now()`, a.nativeTicketHash(ticket)).Scan(&id, &family)
	if e != nil {
		return problem(401, "invalid_ticket", "桥接票据已失效")
	}
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		u, e := a.identity.NativeFamilyUser(ctx, family)
		if e != nil {
			return e
		}
		v, e = a.sessions.Get(ctx, id)
		if e != nil {
			return e
		}
		if sourceIP(r) != v.ClientIP {
			return fault.Denied
		}
		if _, e = a.checkLiveUser(ctx, v, u); e != nil {
			return e
		}
		if e = a.db.Row(ctx, `UPDATE native_bridge_reservations SET consumed_at=now() WHERE session_id=$1 AND ticket_hash=$2 AND consumed_at IS NULL AND expires_at>now() RETURNING host_key,client_key`, id, a.nativeTicketHash(ticket)).Scan(&sealed, &clientKey); e != nil {
			return fault.Proof
		}
		if _, e = a.db.Exec(ctx, `UPDATE native_bridge_reservations SET host_key=''::bytea WHERE session_id=$1`, id); e != nil {
			return e
		}
		return a.sessions.Consume(ctx, id, u.ID, a.nativeTicketHash(ticket), identity.Digest(family))
	})
	if e != nil {
		return problem(401, "invalid_ticket", "桥接票据或资产授权已失效")
	}
	state, reason := "failed", "native_setup_failed"
	defer func() { a.finishSession(id, state, reason) }()
	key, e := secretbox.Open(a.config.EncryptionKey, sealed, "native-ssh:"+id)
	if e != nil {
		return e
	}
	defer clear(key)
	if len(key) != ed25519.PrivateKeySize {
		return fault.Invalid
	}
	signer, e := ssh.NewSignerFromKey(ed25519.PrivateKey(key))
	if e != nil {
		return e
	}
	c, e := ws.Accept(w, r, &ws.AcceptOptions{Subprotocols: []string{"nt-bridge.v1"}, CompressionMode: ws.CompressionDisabled})
	if e != nil {
		return nil
	}
	defer c.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	raw := ws.NetConn(ctx, c, ws.MessageBinary)
	c.SetReadLimit(64 << 10)
	transport := &nativeSSHConn{Conn: raw}
	defer transport.Close()
	l := &liveSession{ctx: ctx, cancel: cancel, started: time.Now(), commandSlot: make(chan struct{}, 1)}
	// The output sink is installed before registration and only used after the
	// channel is accepted. SSH output shares the browser recorder/input tracker.
	var output ssh.Channel
	l.sendOverride = func(typ byte, value string) error {
		if typ != '6' {
			return nil
		}
		b, e := base64.StdEncoding.DecodeString(value)
		if e != nil {
			return e
		}
		if output == nil {
			return io.ErrClosedPipe
		}
		_, e = output.Write(b)
		return e
	}
	a.liveMu.Lock()
	a.live[id] = l
	a.liveMu.Unlock()
	defer func() {
		l.stop("native_closed")
		a.liveMu.Lock()
		delete(a.live, id)
		a.liveMu.Unlock()
		reason = l.reason
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				check, stop := context.WithTimeout(ctx, time.Second)
				_, e := a.checkNativeLive(check, v, family)
				if e == nil {
					e = a.sessions.Renew(check, id)
				}
				stop()
				if e != nil {
					l.stop(sessionLeaseReason(e, "authorization_lost"))
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-done }()
	_ = transport.SetDeadline(time.Now().Add(10 * time.Second))
	cfg := &ssh.ServerConfig{ServerVersion: "SSH-2.0-NextTerminal", PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if meta.User() != "nt" || strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) != clientKey {
			return nil, fault.Denied
		}
		return nil, nil
	}}
	cfg.AddHostKey(signer)
	peer, channels, requests, e := ssh.NewServerConn(transport, cfg)
	if e != nil {
		l.stop("native_ssh_handshake_failed")
		return nil
	}
	defer peer.Close()
	peerDone := make(chan struct{})
	go func() { defer close(peerDone); _ = peer.Wait(); cancel() }()
	defer func() { cancel(); _ = peer.Close(); <-peerDone }()
	_ = transport.SetDeadline(time.Time{})
	go ssh.DiscardRequests(requests)
	setup := time.NewTimer(60 * time.Second)
	defer setup.Stop()
	var incoming ssh.NewChannel
	for incoming == nil {
		select {
		case <-ctx.Done():
			return nil
		case <-setup.C:
			l.stop("native_channel_timeout")
			return nil
		case ch, ok := <-channels:
			if !ok {
				return nil
			}
			if v.Forwarding {
				if ch.ChannelType() != "direct-tcpip" {
					_ = ch.Reject(ssh.Prohibited, "Forward-only connection")
					continue
				}
				a.nativeForwardLoop(l, v, family, ch, channels)
				state = "closed"
				return nil
			}
			if ch.ChannelType() != "session" {
				_ = ch.Reject(ssh.Prohibited, "Only an audited session is permitted")
				continue
			}
			incoming = ch
		}
	}
	output, requests, e = incoming.Accept()
	if e != nil {
		return nil
	}
	defer output.Close()
	go func() {
		for ch := range channels {
			_ = ch.Reject(ssh.Prohibited, "Only one session is permitted")
		}
	}()
	e = a.nativeSSHChannel(l, v, family, output, requests)
	state = "closed"
	if e != nil {
		state = "failed"
		l.stop("native_transport_failed")
	}
	return nil
}

type nativeSSHConn struct{ net.Conn }

func (c *nativeSSHConn) Write(p []byte) (int, error) {
	if e := c.Conn.SetWriteDeadline(time.Now().Add(15 * time.Second)); e != nil {
		return 0, e
	}
	return c.Conn.Write(p)
}

func (a *App) nativeSSHChannel(l *liveSession, v session.Session, family string, ch ssh.Channel, requests <-chan *ssh.Request) error {
	term, cols, rows := "xterm-256color", uint32(80), uint32(24)
	pty := false
	var start *ssh.Request
	command := ""
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for start == nil {
		select {
		case <-l.ctx.Done():
			return l.ctx.Err()
		case <-timer.C:
			return context.DeadlineExceeded
		case req, ok := <-requests:
			if !ok {
				return io.EOF
			}
			if len(req.Payload) > 8192 {
				_ = req.Reply(false, nil)
				continue
			}
			accepted := false
			switch req.Type {
			case "pty-req":
				var p struct {
					Term                      string
					Cols, Rows, Width, Height uint32
					Modes                     string
				}
				if !pty && ssh.Unmarshal(req.Payload, &p) == nil && len(p.Term) <= 64 && !strings.ContainsRune(p.Term, 0) {
					// RFC 4254 §6.2: zero dimensions are unspecified. OpenSSH
					// sends them when forcing a PTY without a local console.
					if p.Cols == 0 {
						p.Cols = cols
					}
					if p.Rows == 0 {
						p.Rows = rows
					}
					if p.Cols < 2 || p.Cols > 500 || p.Rows < 2 || p.Rows > 300 {
						break
					}
					if p.Term != "" {
						term = p.Term
					}
					cols, rows = p.Cols, p.Rows
					pty = true
					accepted = true
				}
			case "shell":
				if len(req.Payload) == 0 {
					start = req
				}
			case "exec":
				var p struct{ Command string }
				if ssh.Unmarshal(req.Payload, &p) == nil && len(p.Command) <= 4096 && strings.TrimSpace(p.Command) != "" && !strings.ContainsRune(p.Command, 0) && !v.Readonly {
					command = p.Command
					start = req
				}
			}
			if start == nil {
				_ = req.Reply(accepted, nil)
			}
		}
	}
	asset, e := a.checkNativeLive(l.ctx, v, family)
	if e != nil {
		_ = start.Reply(false, nil)
		return e
	}
	u := &identity.User{ID: v.ActorID}
	d, e := a.policyDecision(l.ctx, u, v.AssetID, v.ClientIP, "execute", command)
	if e != nil || !d.Allowed || (command == "" && d.Filtered) {
		_ = start.Reply(false, nil)
		return fault.Denied
	}
	var auth ssh.AuthMethod
	var username string
	var trust connectivity.Trust
	e = a.db.Within(l.ctx, func(ctx context.Context) error {
		var e error
		trust, e = a.connectivity.Get(ctx, asset.ID)
		if e != nil {
			return e
		}
		cred, method, e := a.vault.SSHAuth(ctx, asset.CredentialID, v.ActorID, v.ID)
		if e != nil {
			return e
		}
		if cred.Version != v.CredentialVersion {
			return fault.Denied
		}
		username = cred.Username
		auth = method
		return nil
	})
	if e != nil {
		return e
	}
	var dial connectivity.Dialer
	switch {
	case asset.AgentID != "":
		dial = func(ctx context.Context, host string, port int) (net.Conn, error) {
			return a.connectivity.DialAgent(l.ctx, ctx, asset.AgentID, v.ID, host, port, func(ctx context.Context) error { _, e := a.checkNativeLive(ctx, v, family); return e })
		}
	case asset.JumpID != "":
		dial = a.jumpDialer(l.ctx, asset.JumpID, v.ActorID, v.ID)
	case asset.GatewayGroupID != "":
		dial = a.groupDialer(l.ctx, asset.GatewayGroupID, v.ActorID, v.ID, func(ctx context.Context) error { _, e := a.checkNativeLive(ctx, v, family); return e })
	case asset.ProxyID != "":
		dial = a.proxyDialer(l.ctx, asset.ProxyID, v.ActorID, v.ID)
	}
	dialCtx, stop := context.WithTimeout(l.ctx, time.Duration(asset.ConnectTimeout)*time.Second)
	client, e := connectivity.ConnectUsing(dialCtx, trust, username, auth, dial)
	stop()
	if e != nil {
		return e
	}
	defer client.Close()
	closeOnCancel := context.AfterFunc(l.ctx, func() { _ = client.Close() })
	defer closeOnCancel()
	setupTimer := time.AfterFunc(10*time.Second, func() { l.stop("native_setup_timeout") })
	defer setupTimer.Stop()
	a.liveMu.Lock()
	l.client = client
	a.liveMu.Unlock()
	remote, e := client.NewSession()
	if e != nil {
		return e
	}
	defer remote.Close()
	stdin, e := remote.StdinPipe()
	if e != nil {
		return e
	}
	kind := "output"
	if command != "" {
		kind = "exec-output"
	}
	remote.Stdout = &terminalOutput{a: a, live: l, id: v.ID, kind: kind}
	remote.Stderr = remote.Stdout
	if pty {
		if e = remote.RequestPty(term, int(rows), int(cols), ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}); e != nil {
			return e
		}
	}
	var commandID string
	e = a.db.Within(l.ctx, func(ctx context.Context) error {
		// Check again immediately before execution; setup may have taken seconds.
		u, e := a.nativeSessionUser(ctx, v, family)
		if e != nil {
			return e
		}
		if _, e = a.checkLiveUser(ctx, v, u); e != nil {
			return e
		}
		d, e := a.policyDecision(ctx, u, v.AssetID, v.ClientIP, "execute", command)
		if e != nil {
			return e
		}
		if !d.Allowed || (command == "" && d.Filtered) {
			return fault.Denied
		}
		if e = a.sessions.Connected(ctx, v.ID); e != nil {
			return e
		}
		if e = a.audit.AccessState(ctx, v.ID, "connected", "", false); e != nil {
			return e
		}
		if command != "" {
			commandID, e = a.audit.StartCommandInTransaction(ctx, v.ID, v.ActorID, command)
			if e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	code, state := -1, "failed"
	defer func() {
		if commandID != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if a.audit.EndCommand(ctx, commandID, state, code) != nil {
				l.stop("command_audit_failed")
			}
		}
	}()
	if command == "" {
		e = remote.Shell()
	} else {
		e = remote.Start(command)
	}
	if e != nil {
		_ = start.Reply(false, nil)
		return e
	}
	if e = start.Reply(true, nil); e != nil {
		return e
	}
	setupTimer.Stop()
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		defer stdin.Close()
		buf := make([]byte, 16<<10)
		for {
			n, e := ch.Read(buf)
			if n > 0 {
				d, err := a.policyDecision(l.ctx, u, v.AssetID, v.ClientIP, "execute", command)
				if err != nil || !d.Allowed || (command == "" && d.Filtered) {
					l.stop("command_policy_denied")
					return
				}
				if !v.Readonly {
					if err = a.forwardTerminalInput(l, v.ID, v.ActorID, stdin, buf[:n]); err != nil {
						l.stop("native_input_failed")
						return
					}
				}
			}
			if e != nil {
				return
			}
		}
	}()
	defer func() { _ = ch.Close(); <-inputDone }()
	go func() {
		for req := range requests {
			ok := false
			if req.Type == "window-change" && pty && !v.Readonly {
				var p struct{ Cols, Rows, Width, Height uint32 }
				if ssh.Unmarshal(req.Payload, &p) == nil && p.Cols >= 2 && p.Cols <= 500 && p.Rows >= 2 && p.Rows <= 300 {
					l.recordMu.Lock()
					e := a.audit.Record(l.ctx, v.ID, "resize", time.Since(l.started).Milliseconds(), []byte(fmtSize(p.Cols, p.Rows)))
					l.recordMu.Unlock()
					ok = e == nil && remote.WindowChange(int(p.Rows), int(p.Cols)) == nil
					if e != nil {
						l.stop("recording_failed")
					}
				}
			}
			_ = req.Reply(ok, nil)
		}
	}()
	e = remote.Wait()
	code = 0
	state = "succeeded"
	if e != nil {
		code = 255
		state = "failed"
		var exit *ssh.ExitError
		if errors.As(e, &exit) {
			code = exit.ExitStatus()
		}
	}
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{uint32(code)}))
	return nil
}

func fmtSize(cols, rows uint32) string { return fmt.Sprintf("%d,%d", cols, rows) }
