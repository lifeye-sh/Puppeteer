package application

import (
	"context"
	"encoding/base64"
	"github.com/gorilla/websocket"
	"io"
	"net"
	"next-terminal/backend/internal/platform/guac"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRDPClipboardTransportAndReadonly(t *testing.T) {
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
				io.Copy(io.Discard, c)
			}()
		}
	}()
	daemon, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer daemon.Close()
	pasted := make(chan string, 4)
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
				r := guac.NewReader(c)
				if _, e = r.Read(); e != nil {
					return
				}
				io.WriteString(c, guac.Encode("args", "hostname", "port"))
				for i := 0; i < 4; i++ {
					if _, e = r.Read(); e != nil {
						return
					}
				}
				if _, e = r.Read(); e != nil {
					return
				}
				io.WriteString(c, guac.Encode("ready", "fixture")+guac.Encode("clipboard", "5", "text/plain")+guac.Encode("blob", "5", base64.StdEncoding.EncodeToString([]byte("remote-private-text")))+guac.Encode("end", "5")+guac.Encode("sync", "1"))
				for {
					p, e := r.Read()
					if e != nil {
						return
					}
					if p[0] == "blob" && len(p) == 3 {
						index, err := strconv.Atoi(p[1])
						if err != nil || index < 0 || index >= 512 {
							io.WriteString(c, guac.Encode("ack", p[1], "Invalid stream index", "768"))
							continue
						}
						b, _ := base64.StdEncoding.DecodeString(p[2])
						if string(b) == "reject-fixture" {
							io.WriteString(c, guac.Encode("ack", p[1], "Clipboard disabled", "256"))
							continue
						}
						pasted <- string(b)
					}
				}
			}()
		}
	}()
	_, a, srv := r02Server(t, false)
	a.config.GuacdAddr = daemon.Addr().String()
	a.config.GuacdRelayHost = "127.0.0.1"
	admin := loginAs(t, srv, "accept-admin")
	credential := postID(t, srv, "/api/admin/credentials", credInput("clipboard"), admin)
	body := assetInput("RDP clipboard", credential, "")
	body["protocol"] = "rdp"
	body["host"] = "127.0.0.1"
	body["port"] = target.Addr().(*net.TCPAddr).Port
	body["certFingerprint"] = "sha256:" + strings.Repeat("ab", 32)
	asset := postID(t, srv, "/api/admin/assets", body, admin)
	id, ticket := reserve(t, srv, admin, asset, false)
	ws := attach(t, srv, admin, id, ticket)
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, b, e := ws.ReadMessage()
		if e != nil {
			t.Fatal(e)
		}
		if len(b) > 0 && b[0] == '8' {
			text, _ := base64.StdEncoding.DecodeString(string(b[1:]))
			if string(text) != "remote-private-text" {
				t.Fatal("clipboard mismatch")
			}
			break
		}
	}
	if e = ws.WriteMessage(websocket.TextMessage, []byte("8"+base64.StdEncoding.EncodeToString([]byte("local-private-text")))); e != nil {
		t.Fatal(e)
	}
	select {
	case text := <-pasted:
		if text != "local-private-text" {
			t.Fatal(text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("paste missing")
	}
	for _, text := range []string{"中文\r\n第二行", "third paste"} {
		ws.WriteMessage(websocket.TextMessage, []byte("8"+base64.StdEncoding.EncodeToString([]byte(text))))
		select {
		case got := <-pasted:
			if got != text {
				t.Fatal("repeat paste mismatch")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("repeat paste missing")
		}
	}
	ws.WriteMessage(websocket.TextMessage, []byte("8"+base64.StdEncoding.EncodeToString([]byte("reject-fixture"))))
	for {
		_, b, err := ws.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if len(b) > 0 && b[0] == '6' {
			break
		}
	}
	ws.WriteMessage(websocket.TextMessage, []byte("9clipboard-test"))
	for {
		_, b, err := ws.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if string(b) == "9clipboard-test" {
			break
		}
	}
	ws.Close()
	waitClosed(t, a, id)
	recording := call(t, srv.URL, "GET", "/api/sessions/"+id+"/recording", nil, admin, origin)
	if strings.Contains(string(recording.body), "private-text") || strings.Contains(string(recording.body), base64.StdEncoding.EncodeToString([]byte("remote-private-text"))) {
		t.Fatal("clipboard leaked into recording")
	}
	id, ticket = reserve(t, srv, admin, asset, true)
	ws = attach(t, srv, admin, id, ticket)
	defer ws.Close()
	ws.WriteMessage(websocket.TextMessage, []byte("8aGVsbG8="))
	waitClosed(t, a, id)
	select {
	case <-pasted:
		t.Fatal("readonly clipboard bypass")
	default:
	}
	var actor string
	if e = a.db.Row(ctx, "SELECT actor_id::text FROM sessions WHERE id=$1", id).Scan(&actor); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/users/"+actor+"/channels", map[string]any{"upload": true, "download": true, "clipboardCopy": false, "clipboardPaste": false}, admin, origin), 204)
	id, ticket = reserve(t, srv, admin, asset, false)
	ws = attach(t, srv, admin, id, ticket)
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	ws.WriteMessage(websocket.TextMessage, []byte("9policy-test"))
	for {
		_, b, e := ws.ReadMessage()
		if e != nil {
			t.Fatal(e)
		}
		if len(b) > 0 && b[0] == '8' {
			t.Fatal("copy policy bypass")
		}
		if string(b) == "9policy-test" {
			break
		}
	}
	ws.WriteMessage(websocket.TextMessage, []byte("8aGVsbG8="))
	waitClosed(t, a, id)
	select {
	case <-pasted:
		t.Fatal("paste policy bypass")
	default:
	}
}
