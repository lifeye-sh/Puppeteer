package application

import (
	"context"
	"encoding/base64"
	"github.com/gorilla/websocket"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/platform/guac"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestR07GraphicsAuditReadonlyAndTermination(t *testing.T) {
	t.Run("legacy", func(t *testing.T) { testGraphicsAuditReadonlyAndTermination(t, false, false) })
	t.Run("raw", func(t *testing.T) { testGraphicsAuditReadonlyAndTermination(t, true, false) })
}

func testGraphicsAuditReadonlyAndTermination(t *testing.T, rawTransport bool, legacyRDP bool) {
	attach := func(t *testing.T, srv *httptest.Server, c *http.Cookie, id, ticket string) *websocket.Conn {
		if !rawTransport {
			return attach(t, srv, c, id, ticket)
		}
		h := http.Header{"Origin": []string{origin}, "Cookie": []string{c.String()}}
		ws, _, e := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/sessions/"+id+"/ws?graphicsTransport=raw", h)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { ws.Close() })
		if e = ws.WriteMessage(websocket.TextMessage, []byte("3"+ticket)); e != nil {
			t.Fatal(e)
		}
		return ws
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer target.Close()
	go func() {
		for {
			c, e := target.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				stop := context.AfterFunc(ctx, func() { c.Close() })
				defer stop()
				io.Copy(c, c)
			}()
		}
	}()
	daemon, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer daemon.Close()
	var inputs atomic.Int32
	var keepalives atomic.Int32
	go func() {
		for {
			c, e := daemon.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				stop := context.AfterFunc(ctx, func() { c.Close() })
				defer stop()
				reader := guac.NewReader(c)
				if _, e := reader.Read(); e != nil {
					return
				}
				io.WriteString(c, guac.Encode("args", "VERSION_1_1_0", "hostname", "port", "password", "read-only", "security", "ignore-cert"))
				for i := 0; i < 4; i++ {
					if _, e := reader.Read(); e != nil {
						return
					}
				}
				p, e := reader.Read()
				if e != nil || len(p) != 8 {
					return
				}
				if legacyRDP && (p[6] != "rdp" || p[7] != "false") {
					return
				}
				remote, e := net.Dial("tcp", net.JoinHostPort(p[2], p[3]))
				if e != nil {
					return
				}
				defer remote.Close()
				io.WriteString(remote, "route")
				buf := make([]byte, 5)
				if _, e = io.ReadFull(remote, buf); e != nil || string(buf) != "route" {
					return
				}
				io.WriteString(c, guac.Encode("ready", "fixture")+guac.Encode("size", "0", "640", "360")+guac.Encode("sync", "42"))
				for {
					p, e := reader.Read()
					if e != nil {
						return
					}
					if p[0] == "key" {
						inputs.Add(1)
					}
					if p[0] == "nop" {
						keepalives.Add(1)
					}
					io.WriteString(c, guac.Encode("sync", "43"))
				}
			}()
		}
	}()
	_, a, srv := r02Server(t, false)
	a.config.GuacdAddr = daemon.Addr().String()
	a.config.GuacdRelayHost = "127.0.0.1"
	admin := loginAs(t, srv, "accept-admin")
	credential := postID(t, srv, "/api/admin/credentials", credInput("graphics credential"), admin)
	body := assetInput("R07 VNC", credential, "")
	body["protocol"] = "vnc"
	if legacyRDP {
		body["protocol"] = "rdp"
		body["rdpSecurity"] = "rdp"
	}
	body["host"] = "127.0.0.1"
	body["port"] = target.Addr().(*net.TCPAddr).Port
	asset := postID(t, srv, "/api/admin/assets", body, admin)
	readFrame := func(ws *websocket.Conn) {
		t.Helper()
		ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			_, b, e := ws.ReadMessage()
			if e != nil {
				t.Fatal(e)
			}
			if len(b) > 0 && ((!rawTransport && b[0] == '7') || (rawTransport && b[0] == 'G')) {
				return
			}
		}
	}
	send := func(ws *websocket.Conn, p ...string) {
		t.Helper()
		data := "7" + base64.StdEncoding.EncodeToString([]byte(guac.Encode(p...)))
		if rawTransport {
			data = "G" + guac.Encode(p...)
		}
		if e := ws.WriteMessage(websocket.TextMessage, []byte(data)); e != nil {
			t.Fatal(e)
		}
	}
	id, ticket := reserve(t, srv, admin, asset, false)
	ws := attach(t, srv, admin, id, ticket)
	readFrame(ws)
	if _, e = a.db.Exec(ctx, `UPDATE sessions SET last_activity_at=clock_timestamp()-interval '5 minutes' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	// A stalled downstream frame must not block browser liveness reaching guacd.
	a.liveMu.Lock()
	live := a.live[id]
	a.liveMu.Unlock()
	func() {
		live.writeMu.Lock()
		defer live.writeMu.Unlock()
		if e := ws.WriteMessage(websocket.TextMessage, []byte("9heartbeat")); e != nil {
			t.Fatal(e)
		}
		for deadline := time.Now().Add(2 * time.Second); keepalives.Load() == 0 && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
		}
		if keepalives.Load() != 1 {
			t.Fatal("browser heartbeat blocked behind display output or not forwarded to guacd")
		}
	}()
	send(ws, "sync", "42")
	readFrame(ws)
	var oldActivity bool
	if e = a.db.Row(ctx, `SELECT last_activity_at<clock_timestamp()-interval '4 minutes' FROM sessions WHERE id=$1`, id).Scan(&oldActivity); e != nil || !oldActivity {
		t.Fatal("graphics sync extended activity", e)
	}
	send(ws, "key", "65", "1")
	for deadline := time.Now().Add(time.Second); inputs.Load() == 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if inputs.Load() != 1 {
		t.Fatal("key not delivered")
	}
	if e = a.db.Row(ctx, `SELECT last_activity_at>clock_timestamp()-interval '2 seconds' FROM sessions WHERE id=$1`, id).Scan(&oldActivity); e != nil || !oldActivity {
		t.Fatal("graphics key did not refresh activity", e)
	}
	ws.Close()
	waitClosed(t, a, id)
	result := call(t, srv.URL, "GET", "/api/sessions/"+id+"/recording", nil, admin, "")
	expect(t, result, 200)
	if !strings.Contains(string(result.body), "guacamole-events-v1") || strings.Contains(string(result.body), "password") {
		t.Fatal("recording format/secret")
	}
	for _, input := range [][]string{{"key", "65", "1"}, {"clipboard", "1", "text/plain"}, {"size", "800", "600"}} {
		id, ticket = reserve(t, srv, admin, asset, true)
		ws = attach(t, srv, admin, id, ticket)
		readFrame(ws)
		send(ws, input...)
		waitClosed(t, a, id)
		ws.Close()
		if inputs.Load() != 1 {
			t.Fatal("readonly bypass")
		}
	}
	id, ticket = reserve(t, srv, admin, asset, false)
	ws = attach(t, srv, admin, id, ticket)
	readFrame(ws)
	expect(t, call(t, srv.URL, "POST", "/api/sessions/"+id+"/terminate", nil, admin, origin), 204)
	waitClosed(t, a, id)
	ws.Close()
	// Audit quota failure must stop the live transport before forwarding more display data.
	id, ticket = reserve(t, srv, admin, asset, false)
	ws = attach(t, srv, admin, id, ticket)
	readFrame(ws)
	_, e = a.db.Exec(ctx, `UPDATE recordings SET bytes=67108864 WHERE session_id=$1`, id)
	if e != nil {
		t.Fatal(e)
	}
	send(ws, "sync", "42")
	waitClosed(t, a, id)
	ws.Close()
	s, e := a.sessions.Get(ctx, id)
	if e != nil || s.State != "interrupted" {
		t.Fatal("recording failure did not interrupt", s.State, e)
	}
	if os.Getenv("NT_R17_GRAPHICS_LOAD") == "1" {
		ids := make([]string, 4)
		clients := make([]*websocket.Conn, 4)
		for i := range ids {
			sid, ticket := reserve(t, srv, admin, asset, false)
			ids[i] = sid
			clients[i] = attach(t, srv, admin, sid, ticket)
			readFrame(clients[i])
		}
		start := time.Now()
		frames := 0
		for time.Since(start) < time.Minute {
			for _, client := range clients {
				send(client, "key", "65", "1")
				readFrame(client)
				frames++
			}
			time.Sleep(time.Second)
		}
		for i, client := range clients {
			client.Close()
			waitClosed(t, a, ids[i])
			expect(t, call(t, srv.URL, "GET", "/api/sessions/"+ids[i]+"/recording", nil, admin, origin), 200)
		}
		t.Logf("graphics simulated capacity: concurrent=4 input/response rounds=%d elapsed=%s", frames, time.Since(start))
	}
}

func TestR07LegacyRDPAuditReadonlyAndTermination(t *testing.T) {
	testGraphicsAuditReadonlyAndTermination(t, true, true)
}
