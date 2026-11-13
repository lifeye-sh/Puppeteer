package application

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
	"io"
	"log"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/audit"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"strings"
	"sync"
	"time"
)

type liveSession struct {
	ctx                context.Context
	cancel             context.CancelFunc
	once               sync.Once
	reason             string
	client             *ssh.Client
	ws                 *websocket.Conn
	sendOverride       func(byte, string) error // Native SSH output; set before publishing the live session.
	writeMu            sync.Mutex
	recordMu           sync.Mutex
	commandSlot        chan struct{}
	started            time.Time
	clientWriteTimeout time.Duration // Immutable after registration in a.live.
	input              audit.InputTracker
}

func (l *liveSession) stop(reason string) { l.once.Do(func() { l.reason = reason; l.cancel() }) }
func (l *liveSession) send(typ byte, content string) error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	if l.sendOverride != nil {
		return l.sendOverride(typ, content)
	}
	timeout := l.clientWriteTimeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	if e := l.ws.SetWriteDeadline(time.Now().Add(timeout)); e != nil {
		return e
	}
	return l.ws.WriteMessage(websocket.TextMessage, []byte(string(typ)+content))
}
func (a *App) checkLive(ctx context.Context, v session.Session, auth string) (resource.Asset, error) {
	u, e := a.identity.Current(ctx, auth)
	if e != nil {
		return resource.Asset{}, e
	}
	return a.checkLiveUser(ctx, v, u)
}
func (a *App) checkLiveUser(ctx context.Context, v session.Session, u *identity.User) (resource.Asset, error) {
	if u == nil || u.ID != v.ActorID {
		return resource.Asset{}, fault.Denied
	}
	asset, e := a.sessionAsset(ctx, u, v.AssetID)
	if e != nil {
		return asset, e
	}
	decision, e := a.policyDecision(ctx, u, asset.ID, v.ClientIP, "connect", "")
	if e != nil {
		return asset, e
	}
	if !decision.Allowed || (decision.Filtered && asset.Protocol != "ssh") {
		return asset, fault.Denied
	}
	if asset.Version != v.AssetVersion {
		return asset, fault.Denied
	}
	cred, e := a.vault.Get(ctx, asset.CredentialID)
	if e != nil {
		return asset, e
	}
	if cred.Version != v.CredentialVersion {
		return asset, fault.Denied
	}
	if asset.Protocol != v.Protocol {
		return asset, fault.Denied
	}
	if asset.Protocol == "ssh" {
		host, e := a.connectivity.Get(ctx, asset.ID)
		if e != nil {
			return asset, e
		}
		if host.Version != v.HostKeyVersion || host.Host != asset.Host || host.Port != asset.Port {
			return asset, fault.Denied
		}
	}
	current, e := a.sessions.Get(ctx, v.ID)
	if e != nil {
		return asset, e
	}
	if current.Terminate || current.ClosedAt != nil || time.Now().After(current.LeaseExpiresAt) {
		return asset, fault.Denied
	}
	return asset, nil
}
func (a *App) sessionWS(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return fault.Invalid
	}
	v, e := a.sessions.Get(r.Context(), id)
	if e != nil {
		return e
	}
	if v.ActorID != u.ID || v.State != "reserved" {
		return fault.Denied
	}
	if !a.allowAttempt("ws:" + u.ID) {
		return problem(429, "rate_limited", "连接请求过于频繁")
	}
	validOrigin := a.allowsBrowserOrigin(r)
	if !validOrigin {
		return fault.Denied
	}
	upgrader := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, CheckOrigin: func(*http.Request) bool { return validOrigin }}
	ws, e := upgrader.Upgrade(w, r, nil)
	if e != nil {
		return nil
	}
	defer ws.Close()
	ws.SetReadLimit(32768)
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	kind, first, e := ws.ReadMessage()
	if e != nil || kind != websocket.TextMessage || len(first) != 65 || first[0] != '3' {
		return nil
	}
	if e = a.sessions.Consume(r.Context(), id, u.ID, identity.Digest(string(first[1:])), identity.Digest(token(r))); e != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(r.Context())
	l := &liveSession{ctx: ctx, cancel: cancel, ws: ws, commandSlot: make(chan struct{}, 1), started: time.Now()}
	if v.Protocol == "rdp" || v.Protocol == "vnc" {
		l.clientWriteTimeout = 30 * time.Second
	}
	defer cancel()
	a.liveMu.Lock()
	a.live[id] = l
	a.liveMu.Unlock()
	state := "failed"
	defer func() {
		l.stop("client_disconnected")
		if l.client != nil {
			_ = l.client.Close()
		}
		if l.reason == "recording_failed" || l.reason == "command_audit_failed" || l.reason == "server_shutdown" {
			state = "interrupted"
		}
		a.finishSession(id, state, l.reason)
		if v.Protocol == "rdp" || v.Protocol == "vnc" {
			log.Printf("graphics_session_end session=%s protocol=%s state=%s reason=%s duration_ms=%d", id, v.Protocol, state, l.reason, time.Since(l.started).Milliseconds())
		}
		a.liveMu.Lock()
		delete(a.live, id)
		a.liveMu.Unlock()
	}()
	// Closing the socket wakes blocked I/O even when authorization/database refresh fails.
	go func() { <-ctx.Done(); _ = ws.Close() }()
	leaseDone := make(chan struct{})
	go func() {
		defer close(leaseDone)
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				c, stop := context.WithTimeout(ctx, time.Second)
				_, err := a.checkLive(c, v, token(r))
				if err == nil {
					err = a.sessions.Renew(c, id)
				}
				stop()
				if err != nil {
					message := "授权、登录状态或会话租约已失效"
					if errors.Is(err, session.ErrIdle) {
						message = "会话因长时间无操作已断开，请重新连接"
					}
					l.stop(sessionLeaseReason(err, "authorization_or_lease_lost"))
					_ = l.send('0', message)
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-leaseDone }()
	asset, e := a.checkLive(ctx, v, token(r))
	if e != nil {
		l.stop("authorization_lost")
		return nil
	}
	if asset.Protocol == "telnet" {
		return a.telnetSession(r, l, v, asset, &state)
	}
	if asset.Protocol == "rdp" || asset.Protocol == "vnc" {
		return a.graphicsSession(r, l, v, asset, &state)
	}
	host, e := a.connectivity.Get(ctx, asset.ID)
	if e != nil {
		l.stop("host_key_untrusted")
		return nil
	}
	var auth ssh.AuthMethod
	var username string
	e = a.db.Within(ctx, func(ctx context.Context) error {
		cred, method, err := a.vault.SSHAuth(ctx, asset.CredentialID, u.ID, id)
		if err != nil {
			return err
		}
		if cred.Version != v.CredentialVersion {
			return fault.Denied
		}
		auth = method
		username = cred.Username
		return nil
	})
	if e != nil {
		_ = l.send('0', "凭据不可用")
		l.stop("credential_unavailable")
		return nil
	}
	timeout := time.Duration(asset.ConnectTimeout) * time.Second
	dialCtx, stopDial := context.WithTimeout(ctx, timeout)
	var transport connectivity.Dialer
	if asset.AgentID != "" {
		transport = func(dc context.Context, h string, p int) (net.Conn, error) {
			return a.connectivity.DialAgent(ctx, dc, asset.AgentID, id, h, p, func(c context.Context) error { _, e := a.checkLive(c, v, token(r)); return e })
		}
	}
	if asset.JumpID != "" {
		transport = a.jumpDialer(ctx, asset.JumpID, u.ID, id)
	}
	if asset.GatewayGroupID != "" {
		transport = a.groupDialer(ctx, asset.GatewayGroupID, u.ID, id, func(c context.Context) error { _, e := a.checkLive(c, v, token(r)); return e })
	}
	if asset.ProxyID != "" {
		transport = a.proxyDialer(ctx, asset.ProxyID, u.ID, id)
	}
	client, e := connectivity.ConnectUsing(dialCtx, host, username, auth, transport)
	stopDial()
	auth = nil
	if e != nil {
		reason := "ssh_connect_failed"
		message := "SSH 连接或认证失败，请检查主机与凭据"
		if errors.Is(e, connectivity.ErrAgentOffline) {
			reason = "agent_unavailable"
			message = "Agent 离线或不支持拨号，请检查网关及程序版本"
		}
		if errors.Is(e, connectivity.ErrHostKey) {
			reason = "host_key_mismatch"
			message = "SSH 主机密钥已变化，连接已拒绝；请管理员核对"
		}
		_ = l.send('0', message)
		l.stop(reason)
		return nil
	}
	// Only the handler writes client; exec reads it under liveMu after state becomes connected.
	a.liveMu.Lock()
	l.client = client
	a.liveMu.Unlock()
	setupTimer := time.AfterFunc(10*time.Second, func() { l.stop("ssh_setup_timeout") })
	defer setupTimer.Stop()
	clientDone := make(chan struct{})
	go func() { defer close(clientDone); <-ctx.Done(); _ = client.Close() }()
	defer func() { cancel(); <-clientDone }()
	shell, e := client.NewSession()
	if e != nil {
		l.stop("ssh_channel_failed")
		return nil
	}
	defer shell.Close()
	stdin, e := shell.StdinPipe()
	if e != nil {
		l.stop("ssh_channel_failed")
		return nil
	}
	writer := &terminalOutput{a: a, live: l, id: id, kind: "output"}
	shell.Stdout = writer
	shell.Stderr = writer
	if e = shell.RequestPty("xterm-256color", 24, 80, ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}); e != nil {
		_ = l.send('0', "目标拒绝 PTY")
		l.stop("pty_rejected")
		return nil
	}
	if e = shell.Shell(); e != nil {
		l.stop("shell_rejected")
		return nil
	}
	if e = a.db.Within(ctx, func(ctx context.Context) error {
		if e := a.sessions.Connected(ctx, id); e != nil {
			return e
		}
		return a.audit.AccessState(ctx, id, "connected", "", false)
	}); e != nil {
		l.stop("audit_unavailable")
		return nil
	}
	state = "closed"
	setupTimer.Stop()
	if e = l.send('3', id); e != nil {
		l.stop("client_disconnected")
		return nil
	}
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		_ = shell.Wait()
		_ = l.send('4', "远程会话已结束")
		l.stop("remote_closed")
	}()
	defer func() { cancel(); _ = client.Close(); <-waitDone }()
	for {
		_ = ws.SetReadDeadline(time.Now().Add(20 * time.Second))
		kind, data, e := ws.ReadMessage()
		if e != nil {
			return nil
		}
		if kind != websocket.TextMessage || len(data) == 0 {
			l.stop("invalid_frame")
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		switch data[0] {
		case '1':
			d, err := a.policyDecision(ctx, u, asset.ID, v.ClientIP, "execute", "")
			if err != nil {
				return err
			}
			if d.Filtered || !d.Allowed {
				_ = l.send('0', "策略限制终端输入，请使用受控命令执行入口")
				continue
			}
			if v.Readonly {
				_ = l.send('0', "只读会话禁止输入")
				continue
			}
			if e = a.forwardTerminalInput(l, id, u.ID, stdin, data[1:]); e != nil {
				_ = l.send('0', "终端输入审计或传输失败，会话已停止")
				return nil
			}
		case '2':
			if v.Readonly {
				_ = l.send('0', "只读会话禁止调整远端尺寸")
				continue
			}
			parts := strings.Split(string(data[1:]), ",")
			if len(parts) != 2 {
				l.stop("invalid_resize")
				return nil
			}
			cols, e1 := strconv.Atoi(parts[0])
			rows, e2 := strconv.Atoi(parts[1])
			if e1 != nil || e2 != nil || cols < 2 || cols > 500 || rows < 2 || rows > 300 {
				l.stop("invalid_resize")
				return nil
			}
			l.recordMu.Lock()
			e = a.audit.Record(ctx, id, "resize", time.Since(l.started).Milliseconds(), data[1:])
			l.recordMu.Unlock()
			if e != nil {
				l.stop("recording_failed")
				return nil
			}
			if e = shell.WindowChange(rows, cols); e != nil {
				return nil
			}
		case '9':
			if e = l.send('9', string(data[1:])); e != nil {
				return nil
			}
		default:
			l.stop("invalid_frame")
			return nil
		}
	}
}

func (a *App) forwardTerminalInput(l *liveSession, id, actor string, stdin io.Writer, p []byte) error {
	if len(p) == 0 {
		return nil
	}
	if e := a.sessionActivity(l, id); e != nil {
		return e
	}
	// Serialize echo correlation with output. A placeholder must commit before the
	// remote receives Enter, including when echo is disabled or recognition fails.
	l.recordMu.Lock()
	defer l.recordMu.Unlock()
	items, e := l.input.Input(p)
	if e == nil {
		ctx, cancel := context.WithTimeout(l.ctx, 2*time.Second)
		e = a.audit.Submissions(ctx, id, actor, items)
		cancel()
	}
	if e != nil {
		l.stop("command_audit_failed")
		return e
	}
	_, e = stdin.Write(p)
	return e
}

type terminalOutput struct {
	a        *App
	live     *liveSession
	id, kind string
}

func (w *terminalOutput) Write(p []byte) (int, error) {
	// One lock orders recording and browser output across stdout/stderr and explicit exec streams.
	w.live.recordMu.Lock()
	defer w.live.recordMu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	for offset := 0; offset < len(p); {
		end := min(offset+16384, len(p))
		chunk := p[offset:end]
		ctx, cancel := context.WithTimeout(w.live.ctx, 2*time.Second)
		e := w.a.audit.Record(ctx, w.id, w.kind, time.Since(w.live.started).Milliseconds(), chunk)
		cancel()
		if e != nil {
			w.live.stop("recording_failed")
			return offset, e
		}
		if w.kind == "output" {
			for _, submission := range w.live.input.Output(chunk) {
				c, stop := context.WithTimeout(w.live.ctx, 2*time.Second)
				e = w.a.audit.ResolveSubmission(c, submission)
				stop()
				if e != nil {
					w.live.stop("command_audit_failed")
					return offset, e
				}
			}
		}
		if e = w.live.send('6', base64.StdEncoding.EncodeToString(chunk)); e != nil {
			w.live.stop("slow_or_disconnected_client")
			return offset, e
		}
		offset = end
	}
	return len(p), nil
}

type boundedOutput struct {
	mu     sync.Mutex
	writer io.Writer
	n      int
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.n+len(p) > 65536 {
		return 0, errors.New("exec_output_limit")
	}
	w.n += len(p)
	return w.writer.Write(p)
}
func (a *App) executeCommand(w http.ResponseWriter, r *http.Request) error {
	v, u, e := a.inspectSession(r, "identity.manage")
	if e != nil {
		return e
	}
	if v.Protocol != "ssh" {
		return problem(409, "protocol_unsupported", "此协议不支持独立执行命令")
	}
	if v.ActorID != u.ID || v.Readonly || v.State != "connected" {
		return fault.Denied
	}
	var in struct {
		Command string `json:"command"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	if strings.TrimSpace(in.Command) == "" || len(in.Command) > 4096 || strings.ContainsRune(in.Command, 0) {
		return fault.Invalid
	}
	if e = a.enforcePolicy(r.Context(), u, v.AssetID, v.ClientIP, "execute", in.Command); e != nil {
		return e
	}
	if !a.allowAttempt("exec:" + u.ID) {
		return problem(429, "rate_limited", "命令请求过于频繁")
	}
	if _, e = a.checkLive(r.Context(), v, token(r)); e != nil {
		return e
	}
	a.liveMu.Lock()
	l := a.live[v.ID]
	var client *ssh.Client
	if l != nil {
		client = l.client
	}
	a.liveMu.Unlock()
	if l == nil || client == nil {
		return fault.Conflict
	}
	select {
	case l.commandSlot <- struct{}{}:
		defer func() { <-l.commandSlot }()
	default:
		return problem(409, "command_running", "已有命令正在执行")
	}
	if e = a.sessionActivity(l, v.ID); e != nil {
		if errors.Is(e, session.ErrIdle) {
			return problem(409, "session_idle_timeout", "会话空闲超时，请重新连接")
		}
		return e
	}
	id, e := a.audit.StartCommand(r.Context(), v.ID, u.ID, in.Command)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		cmd, err := client.NewSession()
		if err != nil {
			done <- err
			return
		}
		defer cmd.Close()
		output := &boundedOutput{writer: &terminalOutput{a: a, live: l, id: v.ID, kind: "exec-output"}}
		cmd.Stdout = output
		cmd.Stderr = output
		done <- cmd.Run(in.Command)
	}()
	code := 0
	state := "succeeded"
	select {
	case e = <-done:
		if e != nil {
			state = "failed"
			var exit *ssh.ExitError
			if errors.As(e, &exit) {
				code = exit.ExitStatus()
			} else {
				code = -1
			}
		}
	case <-ctx.Done():
		l.stop("command_timeout")
		_ = client.Close()
		<-done
		state = "timeout"
		code = -1
	case <-l.ctx.Done():
		_ = client.Close()
		<-done
		state = "interrupted"
		code = -1
	}
	c, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if e = a.audit.EndCommand(c, id, state, code); e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"id": id, "state": state, "exitCode": code, "message": fmt.Sprintf("命令结束：%s (%d)", state, code)})
	return nil
}
