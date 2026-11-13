package application

import (
	"context"
	"encoding/base64"
	"errors"
	"github.com/gorilla/websocket"
	"io"
	"log"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/modules/storage"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/guac"
	"strconv"
	"strings"
	"sync"
	"time"
)

func graphicsClientFailure(operation string, err error) string {
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "client_" + operation + "_timeout"
	}
	return "client_disconnected"
}

// graphicsRelay is a single-use private TCP bridge. guacd cannot choose another
// destination: the target is already dialed through the authorized asset route.
func graphicsRelay(ctx context.Context, upstream net.Conn, peer net.IP) (*net.TCPListener, error) {
	listener, e := net.ListenTCP("tcp", &net.TCPAddr{Port: 0})
	if e != nil {
		return nil, e
	}
	_ = listener.SetDeadline(time.Now().Add(20 * time.Second))
	go func() {
		defer listener.Close()
		defer upstream.Close()
		stop := context.AfterFunc(ctx, func() { listener.Close(); upstream.Close() })
		defer stop()
		for {
			conn, e := listener.AcceptTCP()
			if e != nil {
				return
			}
			if !conn.RemoteAddr().(*net.TCPAddr).IP.Equal(peer) {
				conn.Close()
				continue
			}
			listener.Close()
			defer conn.Close()
			stopConn := context.AfterFunc(ctx, func() { conn.Close() })
			defer stopConn()
			done := make(chan struct{})
			go func() { io.Copy(upstream, conn); upstream.Close(); conn.Close(); close(done) }()
			io.Copy(conn, upstream)
			conn.Close()
			upstream.Close()
			<-done
			return
		}
	}()
	return listener, nil
}

func (a *App) graphicsSession(r *http.Request, l *liveSession, v session.Session, asset resource.Asset, state *string) error {
	ctx, id := l.ctx, v.ID
	rawTransport := r.URL.Query().Get("graphicsTransport") == "raw"
	fail := func(reason, message string) error { _ = l.send('0', message); l.stop(reason); return nil }
	if a.config.GuacdAddr == "" || a.config.GuacdRelayHost == "" {
		return fail("graphics_unavailable", "图形连接服务未配置")
	}
	dc, cancel := context.WithTimeout(ctx, time.Duration(asset.ConnectTimeout)*time.Second)
	defer cancel()
	control, e := (&net.Dialer{}).DialContext(dc, "tcp", a.config.GuacdAddr)
	if e != nil {
		return fail("guacd_unavailable", "图形连接服务不可用")
	}
	defer control.Close()
	stop := context.AfterFunc(ctx, func() { control.Close() })
	defer stop()
	authorize := func(c context.Context) error { _, e := a.checkLive(c, v, token(r)); return e }
	var transport connectivity.Dialer = connectivity.DialTCP
	switch {
	case asset.AgentID != "":
		transport = func(c context.Context, h string, p int) (net.Conn, error) {
			return a.connectivity.DialAgent(ctx, c, asset.AgentID, id, h, p, authorize)
		}
	case asset.JumpID != "":
		transport = a.jumpDialer(ctx, asset.JumpID, v.ActorID, id)
	case asset.GatewayGroupID != "":
		transport = a.groupDialer(ctx, asset.GatewayGroupID, v.ActorID, id, authorize)
	case asset.ProxyID != "":
		transport = a.proxyDialer(ctx, asset.ProxyID, v.ActorID, id)
	}
	target, e := transport(dc, asset.Host, asset.Port)
	if e != nil {
		return fail("graphics_target_unavailable", "无法连接图形资产，请检查目标端口和网关路径")
	}
	defer target.Close()
	relay, e := graphicsRelay(ctx, target, control.RemoteAddr().(*net.TCPAddr).IP)
	if e != nil {
		return fail("graphics_relay_failed", "无法建立图形连接通道")
	}
	defer relay.Close()
	values := map[string]string{"hostname": a.config.GuacdRelayHost, "port": strconv.Itoa(relay.Addr().(*net.TCPAddr).Port), "read-only": strconv.FormatBool(v.Readonly), "security": "nla", "ignore-cert": "false", "cert-fingerprints": guac.CertificateFingerprints(asset.CertFingerprint), "disable-copy": "true", "disable-paste": "true", "enable-drive": "false", "enable-printing": "false", "enable-audio": "false", "disable-audio": "true", "color-depth": "24", "resize-method": "display-update", "enable-wallpaper": "false"}
	if asset.Protocol == "rdp" && asset.RDPSecurity == "rdp" {
		values["security"] = "rdp"
		delete(values, "cert-fingerprints")
	}
	if asset.Protocol == "rdp" {
		policy, err := storage.New(a.db).Policy(ctx, v.ActorID)
		if err != nil {
			return fail("policy_unavailable", "通道策略不可用")
		}
		values["disable-copy"] = strconv.FormatBool(!policy.Copy)
		values["disable-paste"] = strconv.FormatBool(v.Readonly || !policy.Paste)
		if a.config.RDPDriveEnabled {
			if _, e := storage.New(a.db).BindSession(ctx, v.ActorID, id); e != nil {
				return fail("storage_unavailable", "用户共享存储不可用")
			}
			values["enable-drive"] = "true"
			values["drive-path"] = "/mnt/nt-rdp/" + id
			values["drive-name"] = "NextTerminal"
			values["create-drive-path"] = "false"
			values["disable-upload"] = strconv.FormatBool(v.Readonly || !policy.Upload)
			values["disable-download"] = strconv.FormatBool(!policy.Download)
		}
	}
	e = a.db.Within(ctx, func(c context.Context) error {
		cred, password, e := a.vault.GraphicsAuth(c, asset.CredentialID, v.ActorID, id)
		if e != nil {
			return e
		}
		if cred.Version != v.CredentialVersion {
			return fault.Denied
		}
		values["username"] = cred.Username
		values["password"] = password
		return nil
	})
	if e != nil {
		return fail("credential_unavailable", "图形会话需要有效的密码凭据")
	}
	_ = control.SetDeadline(time.Now().Add(20 * time.Second))
	reader, e := guac.Handshake(control, asset.Protocol, values)
	clear(values)
	if e != nil {
		return fail("graphics_handshake_failed", "图形连接握手失败，请检查服务、认证方式和证书指纹")
	}
	_ = control.SetDeadline(time.Time{})
	if e = a.db.Within(ctx, func(c context.Context) error {
		if e := a.sessions.Connected(c, id); e != nil {
			return e
		}
		return a.audit.AccessState(c, id, "connected", "", false)
	}); e != nil {
		return fail("audit_unavailable", "无法创建会话审计")
	}
	*state = "closed"
	if e = l.send('3', id); e != nil {
		l.stop(graphicsClientFailure("write", e))
		return nil
	}
	var controlMu sync.Mutex
	writeControl := func(parts ...string) error {
		controlMu.Lock()
		defer controlMu.Unlock()
		_ = control.SetWriteDeadline(time.Now().Add(3 * time.Second))
		for _, part := range parts {
			if _, e := io.WriteString(control, part); e != nil {
				return e
			}
		}
		return nil
	}
	var clipboard guac.Clipboard
	batches := guac.NewBatchReader(ctx, reader)
	var instructionCount, batchCount, wireBytes int64
	var recordTime, sendTime time.Duration
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			batch, e := batches.Read()
			if e != nil {
				l.stop("remote_closed")
				return
			}
			var payload []byte
			instructionCount += int64(len(batch))
			for _, instruction := range batch {
				if asset.Protocol == "rdp" {
					// The drive is managed through authenticated storage APIs. Never
					// expose guacd's filesystem-object upload/download API to browsers.
					if instruction[0] == "filesystem" {
						continue
					}
					// guacd only acknowledges clipboard errors, not successful pastes.
					// Surface rejection without leaking it into graphics or recording.
					if len(instruction) == 4 && instruction[0] == "ack" && instruction[1] == guac.ClipboardInputStream {
						if instruction[3] != "0" {
							_ = l.send('6', "剪贴板传输被网关拒绝，请重新连接后重试")
						}
						continue
					}
					if handled, text, reply := clipboard.Handle(instruction); handled {
						if reply != nil {
							if e := writeControl(guac.Encode(reply...)); e != nil {
								l.stop("clipboard_transport")
								return
							}
						}
						policy, err := storage.New(a.db).Policy(ctx, v.ActorID)
						if err != nil {
							l.stop("policy_unavailable")
							return
						}
						if text != nil && policy.Copy && a.enforcePolicy(ctx, &identity.User{ID: v.ActorID}, v.AssetID, v.ClientIP, "copy", "") == nil {
							_ = l.send('8', base64.StdEncoding.EncodeToString(text))
						}
						continue
					}
				}
				// No server-provided file, clipboard or pipe can reach the browser.
				switch instruction[0] {
				case "clipboard", "file", "pipe", "filesystem", "audio", "video":
					l.stop("unsupported_graphics_channel")
					return
				case "error":
					*state = "failed"
					_ = l.send('0', "远端图形连接失败，请检查凭据、服务状态和证书指纹")
					l.stop("graphics_remote_error")
					return
				}
				payload = append(payload, guac.Encode(instruction...)...)
			}
			if len(payload) == 0 {
				continue
			}
			c, finish := context.WithTimeout(ctx, 3*time.Second)
			recordStarted := time.Now()
			e = a.audit.Record(c, id, "graphics", time.Since(l.started).Milliseconds(), payload)
			finish()
			recordTime += time.Since(recordStarted)
			if e != nil {
				l.stop("recording_failed")
				return
			}
			batchCount++
			sendStarted := time.Now()
			if rawTransport {
				wireBytes += int64(1 + len(payload))
				e = l.send('G', string(payload))
			} else {
				wireBytes += int64(1 + base64.StdEncoding.EncodedLen(len(payload)))
				e = l.send('7', base64.StdEncoding.EncodeToString(payload))
			}
			sendTime += time.Since(sendStarted)
			if e != nil {
				l.stop(graphicsClientFailure("write", e))
				return
			}
		}
	}()
	defer func() {
		l.cancel()
		control.Close()
		<-done
		log.Printf("graphics_transport_stats session=%s raw=%t instructions=%d batches=%d wire_bytes_attempted=%d record_ms=%d send_ms=%d", id, rawTransport, instructionCount, batchCount, wireBytes, recordTime.Milliseconds(), sendTime.Milliseconds())
	}()
	l.ws.SetReadLimit(32768)
	for {
		_ = l.ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		kind, data, e := l.ws.ReadMessage()
		if e != nil {
			l.stop(graphicsClientFailure("read", e))
			return nil
		}
		if kind != websocket.TextMessage || len(data) == 0 {
			return fail("invalid_frame", "图形输入格式无效")
		}
		if data[0] == '9' && len(data) <= 32 {
			// Forward actual browser liveness to guacd, whose input idle timeout
			// is independent of our WebSocket deadline. Do not wait behind a slow
			// display write to echo this heartbeat (graphics clients ignore echoes).
			// nop does not acknowledge rendered frames or refresh user activity.
			if e = writeControl(guac.Encode("nop")); e != nil {
				l.stop("graphics_control_write_failed")
				return nil
			}
			continue
		}
		if data[0] == '8' {
			if e = a.enforcePolicy(ctx, &identity.User{ID: v.ActorID}, v.AssetID, v.ClientIP, "paste", ""); e != nil {
				return fail("clipboard_denied", "策略禁止写入剪贴板")
			}
			policy, err := storage.New(a.db).Policy(ctx, v.ActorID)
			if err != nil {
				return err
			}
			if asset.Protocol != "rdp" || v.Readonly || !policy.Paste {
				return fail("clipboard_denied", "只读会话禁止写入剪贴板")
			}
			parts, ok := guac.ClipboardInput(string(data[1:]))
			if !ok {
				return fail("clipboard_limit", "剪贴板仅支持 16 KiB 以内的纯文本")
			}
			if e = a.sessionActivity(l, id); e != nil {
				return nil
			}
			if e := writeControl(parts...); e != nil {
				l.stop("graphics_control_write_failed")
				return nil
			}
			continue
		}
		if data[0] != '7' && !(rawTransport && data[0] == 'G') {
			return fail("invalid_frame", "不支持的图形输入")
		}
		raw := data[1:]
		e = nil
		if data[0] == '7' {
			raw, e = base64.StdEncoding.DecodeString(string(data[1:]))
		}
		if e != nil {
			return fail("invalid_frame", "图形输入编码无效")
		}
		parser := guac.NewReader(strings.NewReader(string(raw)))
		p, e := parser.Read()
		if e != nil {
			return fail("invalid_frame", "图形输入格式无效")
		}
		if string(raw) != guac.Encode(p...) {
			return fail("invalid_frame", "每帧只能包含一条图形指令")
		}
		if !guac.InputAllowed(p, v.Readonly) {
			return fail("graphics_input_denied", "只读或通道策略禁止此操作")
		}
		if p[0] == "key" || p[0] == "mouse" || p[0] == "touch" {
			if e = a.sessionActivity(l, id); e != nil {
				return nil
			}
		}
		if e = writeControl(guac.Encode(p...)); e != nil {
			l.stop("graphics_control_write_failed")
			return nil
		}
	}
}
