package application

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"net"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/resource"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// A real SSH transport fixture (not an HTTP mock); OpenSSH OS-shell acceptance is separate.
func sshTarget(t *testing.T) (string, int, ssh.Signer) {
	return sshTargetRoot(t, "")
}
func sshTargetRoot(t *testing.T, root string) (string, int, ssh.Signer) {
	return sshTargetOptions(t, root, nil)
}
func sshTargetOptions(t *testing.T, root string, forward func(ssh.NewChannel)) (string, int, ssh.Signer) {
	t.Helper()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(key)
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if c.User() == "tester" && string(p) == "remote-only-secret-R03" {
			return nil, nil
		}
		return nil, errors.New("denied")
	}}
	cfg.AddHostKey(signer)
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	var conns []net.Conn
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			raw, e := ln.Accept()
			if e != nil {
				return
			}
			mu.Lock()
			conns = append(conns, raw)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer raw.Close()
				conn, channels, reqs, e := ssh.NewServerConn(raw, cfg)
				if e != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(reqs)
				for c := range channels {
					if c.ChannelType() == "direct-tcpip" && forward != nil {
						forward(c)
						continue
					}
					if c.ChannelType() != "session" {
						_ = c.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					ch, requests, e := c.Accept()
					if e != nil {
						return
					}
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer ch.Close()
						for req := range requests {
							switch req.Type {
							case "subsystem":
								var sub struct{ Name string }
								_ = ssh.Unmarshal(req.Payload, &sub)
								if sub.Name != "sftp" || root == "" {
									_ = req.Reply(false, nil)
									continue
								}
								_ = req.Reply(true, nil)
								server, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(root))
								if err != nil {
									return
								}
								defer server.Close()
								_ = server.Serve()
								return
							case "pty-req":
								_ = req.Reply(true, nil)
							case "window-change":
								var size struct{ Cols, Rows, Width, Height uint32 }
								_ = ssh.Unmarshal(req.Payload, &size)
								_, _ = fmt.Fprintf(ch, "SIZE %dx%d\r\n", size.Cols, size.Rows)
							case "shell":
								_ = req.Reply(true, nil)
								_, _ = ch.Write([]byte("SSH FIXTURE READY\r\nfixture$ "))
								go func() {
									scan := bufio.NewScanner(ch)
									for scan.Scan() {
										if scan.Text() == "hidden-secret" {
											_, _ = fmt.Fprint(ch, "\r\nfixture$ ")
											continue
										}
										_, _ = fmt.Fprintf(ch, "%s\r\nEXECUTED %s\r\nfixture$ ", scan.Text(), scan.Text())
									}
								}()
							case "exec":
								var command struct{ Command string }
								_ = ssh.Unmarshal(req.Payload, &command)
								_ = req.Reply(true, nil)
								_, _ = fmt.Fprintf(ch, "EXEC RESULT %s\r\n", command.Command)
								_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{7}))
								return
							default:
								_ = req.Reply(false, nil)
							}
						}
					}()
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	return host, p, signer
}
func r04Asset(t *testing.T, srv *httptest.Server, admin *http.Cookie, host string, port int, trust bool) string {
	cred := postID(t, srv, "/api/admin/credentials", credInput("R04 credential"), admin)
	body := assetInput("R04 test target", cred, "")
	body["host"] = host
	body["port"] = port
	id := postID(t, srv, "/api/admin/assets", body, admin)
	if trust {
		observation := object(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin))
		key := observation["observed"].(map[string]any)
		key["assetVersion"] = observation["assetVersion"]
		expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id+"/host-key", key, admin, origin), 204)
	}
	return id
}
func reserve(t *testing.T, srv *httptest.Server, c *http.Cookie, asset string, readonly bool) (string, string) {
	r := call(t, srv.URL, "POST", "/api/sessions", map[string]any{"assetId": asset, "readonly": readonly}, c, origin)
	expect(t, r, 200)
	v := object(t, r)
	return v["id"].(string), v["ticket"].(string)
}
func attach(t *testing.T, srv *httptest.Server, c *http.Cookie, id, ticket string) *websocket.Conn {
	t.Helper()
	h := http.Header{"Origin": []string{origin}, "Cookie": []string{c.String()}}
	ws, res, e := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/sessions/"+id+"/ws", h)
	if e != nil {
		t.Fatalf("ws attach: %v %v", e, res)
	}
	t.Cleanup(func() { _ = ws.Close() })
	if e = ws.WriteMessage(websocket.TextMessage, []byte("3"+ticket)); e != nil {
		t.Fatal(e)
	}
	return ws
}
func readUntil(t *testing.T, ws *websocket.Conn, needle string) string {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(8 * time.Second))
	var all strings.Builder
	for {
		_, b, e := ws.ReadMessage()
		if e != nil {
			t.Fatalf("read %q: %v; got %s", needle, e, all.String())
		}
		if len(b) > 0 && b[0] == '6' {
			raw, _ := base64.StdEncoding.DecodeString(string(b[1:]))
			all.Write(raw)
		} else {
			all.Write(b)
		}
		if strings.Contains(all.String(), needle) {
			return all.String()
		}
	}
}
func waitClosed(t *testing.T, a *App, id string) string {
	t.Helper()
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		v, e := a.sessions.Get(context.Background(), id)
		if e == nil && v.ClosedAt != nil {
			return v.Reason
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("session not closed")
	return ""
}

func TestR04SSHResizeExecRecordingAndTermination(t *testing.T) {
	host, port, _ := sshTarget(t)
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	id, ticket := reserve(t, srv, admin, asset, false)
	ws := attach(t, srv, admin, id, ticket)
	readUntil(t, ws, "3"+id)
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1whoami\n"))
	readUntil(t, ws, "EXECUTED whoami")
	_ = ws.WriteMessage(websocket.TextMessage, []byte("2120,40"))
	readUntil(t, ws, "SIZE 120x40")
	r := call(t, srv.URL, "POST", "/api/sessions/"+id+"/commands", map[string]string{"command": "exit 7"}, admin, origin)
	expect(t, r, 200)
	if object(t, r)["exitCode"] != float64(7) {
		t.Fatal(string(r.body))
	}
	readUntil(t, ws, "EXEC RESULT exit 7")
	expect(t, call(t, srv.URL, "POST", "/api/sessions/"+id+"/terminate", nil, admin, origin), 204)
	if got := waitClosed(t, a, id); got != "terminated" {
		t.Fatal(got)
	}
	var chunks, resize, commands, access int
	var data string
	e := p.QueryRow(context.Background(), `SELECT count(*),count(*) FILTER(WHERE kind='resize'),COALESCE(string_agg(convert_from(payload,'UTF8'),''),'') FROM recording_chunks WHERE session_id=$1`, id).Scan(&chunks, &resize, &data)
	if e != nil || chunks < 3 || resize != 1 || !strings.Contains(data, "EXECUTED whoami") {
		t.Fatalf("recording %d %d %q %v", chunks, resize, data, e)
	}
	_ = p.QueryRow(context.Background(), `SELECT count(*) FROM session_commands WHERE session_id=$1 AND exit_code=7 AND state='failed'`, id).Scan(&commands)
	_ = p.QueryRow(context.Background(), `SELECT count(*) FROM access_logs WHERE session_id=$1 AND state='closed' AND reason='terminated'`, id).Scan(&access)
	if commands != 1 || access != 1 {
		t.Fatal(commands, access)
	}
	if strings.Contains(data, "remote-only-secret") {
		t.Fatal("credential leaked")
	}
}
func TestR04ScopeTicketsReadonlyAndRevocation(t *testing.T) {
	host, port, _ := sshTarget(t)
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	uid := postID(t, srv, "/api/admin/users", userInput("ssh-user", nil, nil), admin)
	postID(t, srv, "/api/admin/users", userInput("ssh-denied", nil, nil), admin)
	u := loginAs(t, srv, "ssh-user")
	denied := loginAs(t, srv, "ssh-denied")
	expect(t, call(t, srv.URL, "POST", "/api/sessions", map[string]any{"assetId": asset}, u, origin), 403)
	grant := postID(t, srv, "/api/admin/grants", map[string]string{"userId": uid, "resourceId": asset}, admin)
	id, ticket := reserve(t, srv, u, asset, true)
	for _, c := range []*http.Cookie{denied, admin} {
		expect(t, call(t, srv.URL, "GET", "/api/sessions/"+id+"/ws", nil, c, origin), 403)
	}
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+id, nil, denied, origin), 403)
	expect(t, call(t, srv.URL, "GET", "/api/sessions?all=true", nil, u, origin), 403)
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+id+"/ws", nil, u, "https://evil.example"), 403)
	// Same user in a different login session cannot consume a stolen ticket.
	otherLogin := loginAs(t, srv, "ssh-user")
	bad := attach(t, srv, otherLogin, id, ticket)
	_ = bad.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, e := bad.ReadMessage(); e == nil {
		t.Fatal("ticket not bound to login")
	}
	bad.Close()
	ws := attach(t, srv, u, id, ticket)
	readUntil(t, ws, "3"+id)
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+id+"/ws", nil, u, origin), 403)
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1touch unsafe\n"))
	readUntil(t, ws, "只读会话禁止输入")
	_ = ws.WriteMessage(websocket.TextMessage, []byte("2100,40"))
	readUntil(t, ws, "只读会话禁止调整")
	expect(t, call(t, srv.URL, "POST", "/api/sessions/"+id+"/commands", map[string]string{"command": "whoami"}, u, origin), 403)
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/grants/"+grant, nil, admin, origin), 204)
	if reason := waitClosed(t, a, id); reason != "authorization_or_lease_lost" {
		t.Fatal(reason)
	}
}
func TestR04HostKeyAndCredentialFailures(t *testing.T) {
	host, port, _ := sshTarget(t)
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, false)
	missing := call(t, srv.URL, "GET", "/api/admin/assets/"+asset+"/host-key", nil, admin, origin)
	expect(t, missing, 200)
	if strings.TrimSpace(string(missing.body)) != "null" {
		t.Fatal("missing trust must be JSON null")
	}
	expect(t, call(t, srv.URL, "POST", "/api/sessions", map[string]string{"assetId": asset}, admin, origin), 409)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	wrong, _ := ssh.NewSignerFromKey(other)
	body := map[string]any{"host": host, "port": port, "publicKey": string(ssh.MarshalAuthorizedKey(wrong.PublicKey())), "assetVersion": 1, "version": 0}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+asset+"/host-key", body, admin, origin), 204)
	id, ticket := reserve(t, srv, admin, asset, false)
	ws := attach(t, srv, admin, id, ticket)
	readUntil(t, ws, "主机密钥已变化")
	if got := waitClosed(t, a, id); got != "host_key_mismatch" {
		t.Fatal(got)
	}
	observation := object(t, call(t, srv.URL, "POST", "/api/admin/assets/"+asset+"/host-key/discover", nil, admin, origin))
	key := observation["observed"].(map[string]any)
	key["assetVersion"] = 1
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+asset+"/host-key", key, admin, origin), 204)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+asset+"/host-key", key, admin, origin), 409)
	rows, _, _ := a.resources.List(context.Background(), resource.Filter{All: true, ID: asset, Limit: 1})
	cred, _ := a.vault.Get(context.Background(), rows[0].CredentialID)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/credentials/"+cred.ID, map[string]any{"name": cred.Name, "username": cred.Username, "kind": cred.Kind, "version": cred.Version, "secret": map[string]string{"password": "wrong-remote-password"}}, admin, origin), 200)
	id, ticket = reserve(t, srv, admin, asset, false)
	ws = attach(t, srv, admin, id, ticket)
	readUntil(t, ws, "SSH 连接或认证失败")
	if got := waitClosed(t, a, id); got != "ssh_connect_failed" {
		t.Fatal(got)
	}
}
func TestR04ExpiredLeaseAndRecordingFailure(t *testing.T) {
	host, port, _ := sshTarget(t)
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.RunSessionMaintenance(ctx) }()
	defer func() { stop(); <-done }()
	id, _ := reserve(t, srv, admin, asset, false)
	_, _ = p.Exec(context.Background(), `UPDATE sessions SET ticket_expires_at=now()-interval '1 second',lease_expires_at=now()-interval '1 second' WHERE id=$1`, id)
	if got := waitClosed(t, a, id); got != "lease_expired" {
		t.Fatal(got)
	}
	id, ticket := reserve(t, srv, admin, asset, false)
	ws := attach(t, srv, admin, id, ticket)
	readUntil(t, ws, "3"+id)
	_, _ = p.Exec(context.Background(), `UPDATE recordings SET bytes=67108864 WHERE session_id=$1`, id)
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1quota\n"))
	if got := waitClosed(t, a, id); got != "recording_failed" {
		t.Fatal(got)
	}
	var state string
	_ = p.QueryRow(context.Background(), `SELECT state FROM recordings WHERE session_id=$1`, id).Scan(&state)
	if state == "writing" {
		t.Fatal("unfinished recording")
	}
	// Address filtering also applies to discovery and SSH dialing, before credentials are sent.
	_, e := connectivity.Discover(context.Background(), "169.254.169.254", 22)
	if e == nil {
		t.Fatal("metadata target accepted")
	}
}

func TestR04ReservationLimitsExpiryAndWebsiteLogout(t *testing.T) {
	host, port, _ := sshTarget(t)
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	ids := []string{}
	for i := 0; i < 4; i++ {
		id, _ := reserve(t, srv, admin, asset, false)
		ids = append(ids, id)
	}
	expect(t, call(t, srv.URL, "POST", "/api/sessions", map[string]string{"assetId": asset}, admin, origin), 409)
	for _, id := range ids {
		a.finishSession(id, "interrupted", "test_cleanup")
	}
	id, ticket := reserve(t, srv, admin, asset, false)
	_, _ = p.Exec(context.Background(), `UPDATE sessions SET ticket_expires_at=now()-interval '1 second' WHERE id=$1`, id)
	ws := attach(t, srv, admin, id, ticket)
	_ = ws.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, e := ws.ReadMessage(); e == nil {
		t.Fatal("expired ticket accepted")
	}
	ws.Close()
	a.finishSession(id, "interrupted", "test_cleanup")
	id, ticket = reserve(t, srv, admin, asset, false)
	ws = attach(t, srv, admin, id, ticket)
	readUntil(t, ws, "3"+id)
	expect(t, call(t, srv.URL, "POST", "/api/logout", nil, admin, origin), 204)
	if got := waitClosed(t, a, id); got != "authorization_or_lease_lost" {
		t.Fatal(got)
	}
}
