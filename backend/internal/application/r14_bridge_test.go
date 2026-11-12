package application

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	ws "github.com/coder/websocket"
	"golang.org/x/crypto/ssh"
	"io"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/nativeclient"
	"strings"
	"testing"
	"time"
)

// Isolated fixture only: browser consent/PKCE is covered by the R14 OAuth tests.
func nativeFixtureToken(t *testing.T, a *App, browser *http.Cookie, scopes []string) string {
	t.Helper()
	var access string
	e := a.db.Within(context.Background(), func(ctx context.Context) error {
		if e := nativeFixtureSetting(ctx, a, true); e != nil {
			return e
		}
		u, e := a.identity.Current(ctx, browser.Value)
		if e != nil {
			return e
		}
		c, e := a.identity.OAuthClient(ctx, identity.NativeClientUUID, false)
		if e != nil {
			return e
		}
		settings, e := a.identity.OAuthSettings(ctx)
		if e != nil {
			return e
		}
		var version, epoch int64
		if e = a.db.Row(ctx, `SELECT u.version,i.auth_epoch FROM users u CROSS JOIN instance_state i WHERE u.id=$1`, u.ID).Scan(&version, &epoch); e != nil {
			return e
		}
		code, e := a.identity.OAuthCode(ctx, identity.OAuthPending{UserID: u.ID, ClientID: c.ID, ClientVersion: c.Version, ServerVersion: settings.Version, AuthEpoch: epoch, Request: identity.OAuthAuthorization{Scopes: scopes, RedirectURI: "http://127.0.0.1:54321/callback", Challenge: strings.Repeat("x", 43)}}, version)
		if e != nil {
			return e
		}
		g, _, _, _, e := a.identity.ConsumeOAuthCode(ctx, c.ID, code)
		if e != nil {
			return e
		}
		g, e = a.identity.CreateOAuthFamily(ctx, g)
		if e != nil {
			return e
		}
		access, _, _, e = a.identity.IssueOAuthTokens(ctx, g, false)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return access
}

func TestR14NativeSSHBridge(t *testing.T) {
	host, port, _ := sshTarget(t)
	pool, a, server := r02Server(t, false)
	browser := loginAs(t, server, "accept-admin")
	asset := r04Asset(t, server, browser, host, port, true)
	tlsServer := httptest.NewTLSServer(server.Config.Handler)
	defer tlsServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	request := func(method, path, token string, body any) result {
		b, _ := json.Marshal(body)
		r, _ := http.NewRequestWithContext(ctx, method, tlsServer.URL+path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		res, e := tlsServer.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		return result{status: res.StatusCode, body: data}
	}
	keys := map[string]ssh.Signer{}
	sessionKeys := map[string]ssh.Signer{}
	input := func() map[string]any {
		_, priv, _ := ed25519.GenerateKey(rand.Reader)
		signer, _ := ssh.NewSignerFromKey(priv)
		key := identity.NewToken()
		keys[key] = signer
		return map[string]any{"assetId": asset, "entry": "ssh", "idempotencyKey": key, "clientVersion": "acceptance", "clientPublicKey": strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))}
	}
	readOnlyToken := nativeFixtureToken(t, a, browser, []string{"assets:read"})
	expect(t, request("POST", "/api/native/v1/connect", readOnlyToken, input()), 403)
	token := nativeFixtureToken(t, a, browser, []string{"assets:read", "sessions:connect"})
	reserve := func(in map[string]any) map[string]any {
		r := request("POST", "/api/native/v1/connect", token, in)
		expect(t, r, 200)
		out := object(t, r)
		sessionKeys[out["sessionId"].(string)] = keys[in["idempotencyKey"].(string)]
		return out
	}
	connect := func(res map[string]any) (*ssh.Client, *ws.Conn) {
		headers := http.Header{}
		headers.Set("Authorization", "Bearer "+res["bridgeTicket"].(string))
		socket, _, e := ws.Dial(ctx, "wss"+strings.TrimPrefix(tlsServer.URL, "https")+"/api/native/v1/bridge", &ws.DialOptions{HTTPClient: tlsServer.Client(), HTTPHeader: headers, Subprotocols: []string{"nt-bridge.v1"}})
		if e != nil {
			t.Fatal(e)
		}
		key, _, _, _, e := ssh.ParseAuthorizedKey([]byte(res["hostKey"].(string)))
		if e != nil {
			t.Fatal(e)
		}
		raw := ws.NetConn(ctx, socket, ws.MessageBinary)
		client, ch, req, e := ssh.NewClientConn(raw, "native", &ssh.ClientConfig{User: "nt", HostKeyCallback: ssh.FixedHostKey(key), Auth: []ssh.AuthMethod{ssh.PublicKeys(sessionKeys[res["sessionId"].(string)])}})
		if e != nil {
			socket.CloseNow()
			t.Fatal(e)
		}
		return ssh.NewClient(client, ch, req), socket
	}
	in := input()
	reservation := reserve(in)
	replay := reserve(in)
	if replay["bridgeTicket"] != nil || replay["sessionId"] != reservation["sessionId"] {
		t.Fatal("idempotency leaked/reissued ticket")
	}
	client, socket := connect(reservation)
	defer client.Close()
	defer socket.CloseNow()
	// A ticket is single use even while the first SSH connection is active.
	h := http.Header{}
	h.Set("Authorization", "Bearer "+reservation["bridgeTicket"].(string))
	dup, resp, e := ws.Dial(ctx, "wss"+strings.TrimPrefix(tlsServer.URL, "https")+"/api/native/v1/bridge", &ws.DialOptions{HTTPClient: tlsServer.Client(), HTTPHeader: h, Subprotocols: []string{"nt-bridge.v1"}})
	if e == nil {
		dup.CloseNow()
		t.Fatal("ticket replay accepted")
	}
	if resp == nil || resp.StatusCode != 401 {
		t.Fatal("ticket replay response")
	}
	if conn, e := client.Dial("tcp", "127.0.0.1:22"); e == nil {
		conn.Close()
		t.Fatal("TCP forwarding permitted")
	}
	if ok, _, e := client.SendRequest("tcpip-forward", true, ssh.Marshal(struct {
		Host string
		Port uint32
	}{"127.0.0.1", 0})); e != nil || ok {
		t.Fatal("remote forwarding policy", e)
	}
	command, e := client.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	if e = command.RequestSubsystem("sftp"); e == nil {
		t.Fatal("SFTP bypass permitted")
	}
	output, e := command.CombinedOutput("whoami")
	var exit *ssh.ExitError
	if !errors.As(e, &exit) || exit.ExitStatus() != 7 || !strings.Contains(string(output), "EXEC RESULT whoami") {
		t.Fatalf("exec/exit forwarding: %q %v", output, e)
	}
	client.Close()
	socket.CloseNow()
	var count int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM session_commands WHERE session_id=$1 AND command='whoami'`, reservation["sessionId"]).Scan(&count); e != nil || count != 1 {
		t.Fatal("explicit command audit", count, e)
	}
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM recording_chunks WHERE session_id=$1`, reservation["sessionId"]).Scan(&count); e != nil || count == 0 {
		t.Fatal("missing recording", e)
	}
	expect(t, request("DELETE", "/api/native/v1/sessions/"+reservation["sessionId"].(string), token, nil), 204)
	expect(t, request("DELETE", "/api/native/v1/sessions/"+reservation["sessionId"].(string), token, nil), 204)
	// Interactive input uses the same privacy-preserving submission tracker.
	native, e := nativeclient.New(tlsServer.URL, tlsServer.Client().Transport)
	if e != nil {
		t.Fatal(e)
	}
	cli, e := native.ConnectSSH(ctx, nativeclient.Token{Access: token}, asset)
	if e != nil {
		t.Fatal(e)
	}
	cliCommand, e := cli.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	cliOutput, e := cliCommand.CombinedOutput("hostname")
	if !errors.As(e, &exit) || exit.ExitStatus() != 7 || !strings.Contains(string(cliOutput), "EXEC RESULT hostname") {
		t.Fatal("CLI SSH bridge", e)
	}
	cli.Close()
	wrong := reserve(input())
	wrongHeaders := http.Header{}
	wrongHeaders.Set("Authorization", "Bearer "+wrong["bridgeTicket"].(string))
	wrongSocket, _, e := ws.Dial(ctx, "wss"+strings.TrimPrefix(tlsServer.URL, "https")+"/api/native/v1/bridge", &ws.DialOptions{HTTPClient: tlsServer.Client(), HTTPHeader: wrongHeaders, Subprotocols: []string{"nt-bridge.v1"}})
	if e != nil {
		t.Fatal(e)
	}
	wrongHost, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(wrong["hostKey"].(string)))
	unauth, _, _, e := ssh.NewClientConn(ws.NetConn(ctx, wrongSocket, ws.MessageBinary), "native", &ssh.ClientConfig{User: "nt", HostKeyCallback: ssh.FixedHostKey(wrongHost)})
	if e == nil {
		unauth.Close()
		t.Fatal("ticket alone bypassed one-use SSH key")
	}
	wrongSocket.CloseNow()
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM native_bridge_reservations WHERE session_id=$1 AND consumed_at IS NOT NULL AND octet_length(host_key)=0`, wrong["sessionId"]).Scan(&count); e != nil || count != 1 {
		t.Fatal("failed handshake did not burn ticket/scrub key", e)
	}
	live := reserve(input())
	shellClient, shellSocket := connect(live)
	defer shellClient.Close()
	defer shellSocket.CloseNow()
	sh, e := shellClient.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	stdout, _ := sh.StdoutPipe()
	stdin, _ := sh.StdinPipe()
	if e = sh.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); e != nil {
		t.Fatal(e)
	}
	if e = sh.Shell(); e != nil {
		t.Fatal(e)
	}
	reader := bufio.NewReader(stdout)
	if line, e := reader.ReadString('\n'); e != nil || !strings.Contains(line, "READY") {
		t.Fatal("shell startup", line, e)
	}
	if e = sh.WindowChange(30, 100); e != nil {
		t.Fatal(e)
	}
	for attempt := 0; attempt < 40; attempt++ {
		if e = pool.QueryRow(ctx, `SELECT count(*) FROM recording_chunks WHERE session_id=$1 AND kind='resize' AND convert_from(payload,'UTF8')='100,30'`, live["sessionId"]).Scan(&count); e != nil {
			t.Fatal(e)
		}
		if count == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if count != 1 {
		t.Fatal("native PTY resize was not recorded")
	}
	_, _ = stdin.Write([]byte("whoami\n"))
	for {
		line, e := reader.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(line, "EXECUTED whoami") {
			break
		}
	}
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM session_commands WHERE session_id=$1 AND command='whoami' AND source='terminal-input' AND state='observed'`, live["sessionId"]).Scan(&count); e != nil || count != 1 {
		t.Fatal("interactive audit", count, e)
	}
	if extra, e := shellClient.NewSession(); e == nil {
		extra.Close()
		t.Fatal("second session permitted")
	}
	// Expired access cannot create a connection, but must not terminate an
	// established, independently revalidated authorization-family lease.
	if _, e = pool.Exec(ctx, `UPDATE oauth_tokens SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, identity.Digest(token)); e != nil {
		t.Fatal(e)
	}
	expect(t, request("POST", "/api/native/v1/connect", token, input()), 401)
	time.Sleep(2300 * time.Millisecond)
	_, _ = stdin.Write([]byte("hostname\n"))
	for {
		line, e := reader.ReadString('\n')
		if e != nil {
			t.Fatal("valid family lease lost with access token expiry", e)
		}
		if strings.Contains(line, "EXECUTED hostname") {
			break
		}
	}
	// Family revocation closes a live SSH stream within the normal lease window.
	if _, e = pool.Exec(ctx, `UPDATE oauth_token_families SET revoked_at=now() WHERE id=(SELECT family_id FROM oauth_tokens WHERE token_hash=$1)`, identity.Digest(token)); e != nil {
		t.Fatal(e)
	}
	closed := make(chan struct{})
	go func() { _ = shellClient.Wait(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked native stream stayed open")
	}
}
