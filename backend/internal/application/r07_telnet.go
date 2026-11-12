package application

import (
	"context"
	"github.com/gorilla/websocket"
	"io"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/platform/telnet"
	"strconv"
	"strings"
	"time"
)

// Uses the same ticket, authorization renewal, audit and finalization as SSH.
func (a *App) telnetSession(r *http.Request, l *liveSession, v session.Session, asset resource.Asset, state *string) error {
	id, ctx := v.ID, l.ctx
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
	dc, done := context.WithTimeout(ctx, time.Duration(asset.ConnectTimeout)*time.Second)
	raw, e := transport(dc, asset.Host, asset.Port)
	done()
	if e != nil {
		_ = l.send('0', "Telnet 连接失败，请检查目标地址、端口和连接路径")
		l.stop("telnet_connect_failed")
		return nil
	}
	conn := telnet.New(raw)
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if e = a.db.Within(ctx, func(c context.Context) error {
		if e := a.sessions.Connected(c, id); e != nil {
			return e
		}
		return a.audit.AccessState(c, id, "connected", "", false)
	}); e != nil {
		l.stop("audit_unavailable")
		return nil
	}
	*state = "closed"
	if e = l.send('3', id); e != nil {
		return nil
	}
	output := &terminalOutput{a: a, live: l, id: id, kind: "output"}
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(output, conn)
		_ = l.send('4', "Telnet 连接已结束")
		l.stop("remote_closed")
	}()
	defer func() { l.cancel(); conn.Close(); <-copied }()
	for {
		_ = l.ws.SetReadDeadline(time.Now().Add(20 * time.Second))
		kind, data, e := l.ws.ReadMessage()
		if e != nil {
			return nil
		}
		if kind != websocket.TextMessage || len(data) == 0 {
			l.stop("invalid_frame")
			return nil
		}
		switch data[0] {
		case '1':
			if e = a.enforcePolicy(ctx, &identity.User{ID: v.ActorID}, v.AssetID, v.ClientIP, "execute", ""); e != nil {
				_ = l.send('0', "策略禁止终端输入")
				continue
			}
			if v.Readonly {
				_ = l.send('0', "只读会话禁止输入")
				continue
			}
			if e = a.forwardTerminalInput(l, id, v.ActorID, conn, data[1:]); e != nil {
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
			if e = conn.Resize(cols, rows); e != nil {
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
