package application

import (
	"bufio"
	"context"
	"github.com/gorilla/websocket"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestR07TelnetSessionAuditReadonlyAndTermination(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	var input atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			conn, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				stop := context.AfterFunc(ctx, func() { conn.Close() })
				defer stop()
				_, _ = io.WriteString(conn, "Telnet fixture ready\r\n")
				reader := bufio.NewReader(conn)
				for {
					line, e := reader.ReadString('\n')
					if e != nil {
						return
					}
					input.Add(1)
					_, _ = io.WriteString(conn, strings.TrimSpace(line)+"\r\naccepted\r\n")
				}
			}()
		}
	}()
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	credential := postID(t, srv, "/api/admin/credentials", credInput("telnet test credential"), admin)
	body := assetInput("R07 Telnet", credential, "")
	body["protocol"] = "telnet"
	body["host"] = "127.0.0.1"
	body["port"] = ln.Addr().(*net.TCPAddr).Port
	asset := postID(t, srv, "/api/admin/assets", body, admin)
	// No SSH host key is required, and discovery is explicitly rejected.
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+asset+"/host-key/discover", nil, admin, origin), 409)
	id, ticket := reserve(t, srv, admin, asset, false)
	ws := attach(t, srv, admin, id, ticket)
	readUntil(t, ws, "Telnet fixture ready")
	if e = ws.WriteMessage(websocket.TextMessage, []byte("1hello\r")); e != nil {
		t.Fatal(e)
	}
	readUntil(t, ws, "accepted")
	expect(t, call(t, srv.URL, "POST", "/api/sessions/"+id+"/commands", map[string]string{"command": "pwd"}, admin, origin), 409)
	ws.Close()
	waitClosed(t, a, id)
	r := call(t, srv.URL, "GET", "/api/sessions/"+id+"/recording?after=0", nil, admin, "")
	expect(t, r, 200)
	if !strings.Contains(string(r.body), "complete") {
		t.Fatal("recording not complete")
	}
	var commands int
	if e = a.db.Row(context.Background(), `SELECT count(*) FROM session_commands WHERE session_id=$1`, id).Scan(&commands); e != nil || commands == 0 {
		t.Fatalf("submission audit %d %v", commands, e)
	}
	id, ticket = reserve(t, srv, admin, asset, true)
	ro := attach(t, srv, admin, id, ticket)
	readUntil(t, ro, "Telnet fixture ready")
	baseline := input.Load()
	_ = ro.WriteMessage(websocket.TextMessage, []byte("1forbidden\r"))
	readUntil(t, ro, "只读会话禁止输入")
	time.Sleep(50 * time.Millisecond)
	if input.Load() != baseline {
		t.Fatal("readonly input reached remote")
	}
	expect(t, call(t, srv.URL, "POST", "/api/sessions/"+id+"/terminate", nil, admin, origin), 204)
	waitClosed(t, a, id)
}
