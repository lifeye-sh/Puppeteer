package application

import (
	"bufio"
	"context"
	"golang.org/x/crypto/ssh"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/nativeclient"
	"strings"
	"testing"
	"time"
)

func TestR14NativeDevices(t *testing.T) {
	host, port, _ := sshTarget(t)
	pool, a, srv := r02Server(t, false)
	owner := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("device-other", nil, nil), owner)
	other := loginAs(t, srv, "device-other")
	asset := r04Asset(t, srv, owner, host, port, true)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	first := nativeFixtureToken(t, a, owner, []string{"assets:read", "sessions:connect", "offline_access"})
	second := nativeFixtureToken(t, a, owner, []string{"assets:read", "sessions:connect"})
	nativeFixtureToken(t, a, other, []string{"assets:read"})
	var firstID, secondID, refresh string
	e := a.db.Within(ctx, func(c context.Context) error {
		g, _, e := a.identity.OAuthToken(c, first, "access", identity.NativeClientUUID, false)
		if e != nil {
			return e
		}
		firstID = g.ID
		if _, e = a.identity.NativeUser(c, first, "sessions:connect"); e != nil {
			return e
		} // second login must not revoke first
		if e = a.identity.DescribeNativeDevice(c, g, identity.NativeDeviceInfo{Name: "办公电脑", OS: "windows", Arch: "amd64", ClientVersion: "test-1"}, "127.0.0.1"); e != nil {
			return e
		}
		_, refresh, _, e = a.identity.IssueOAuthTokens(c, g, true)
		if e != nil {
			return e
		}
		g, _, e = a.identity.OAuthToken(c, second, "access", identity.NativeClientUUID, false)
		secondID = g.ID
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE native_client_devices SET last_seen_at=NULL WHERE family_id=$1`, firstID); e != nil {
		t.Fatal(e)
	}
	list := func(cookie *http.Cookie) []any {
		r := call(t, srv.URL, "GET", "/api/account/native-devices", nil, cookie, origin)
		expect(t, r, 200)
		return object(t, r)["items"].([]any)
	}
	if len(list(owner)) != 2 || len(list(other)) != 1 {
		t.Fatal("cross-user device list")
	}
	for _, raw := range list(owner) {
		v := raw.(map[string]any)
		if v["id"] == firstID && v["lastSeenAt"] != nil {
			t.Fatal("unknown activity was invented")
		}
		if v["status"] != "active" {
			t.Fatal("independent login invalidated another device")
		}
		if v["id"] == firstID && (v["name"] != "办公电脑" || v["os"] != "windows") {
			t.Fatal("device metadata missing")
		}
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/native-devices?page=0", nil, owner, origin), 422)
	expect(t, call(t, srv.URL, "GET", "/api/account/native-devices", nil, nil, origin), 401)
	expect(t, call(t, srv.URL, "DELETE", "/api/account/native-devices/"+firstID, map[string]string{"password": testPassword}, other, origin), 403)
	expect(t, call(t, srv.URL, "DELETE", "/api/account/native-devices/"+firstID, map[string]string{"password": "wrong"}, owner, origin), 400)
	postToken := func(value string) result {
		form := url.Values{"client_id": {identity.NativeClientID}, "grant_type": {"refresh_token"}, "refresh_token": {value}, "device_name": {"must-not-change-on-refresh"}}
		req, _ := http.NewRequest("POST", srv.URL+"/api/oidc/server/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return result{status: res.StatusCode, body: b}
	}
	renewed := postToken(refresh)
	expect(t, renewed, 200)
	renewedToken := object(t, renewed)
	if renewedToken["device_id"] != firstID {
		t.Fatal("refresh changed device identity")
	}
	var name string
	if e = pool.QueryRow(ctx, `SELECT name FROM native_client_devices WHERE family_id=$1`, firstID).Scan(&name); e != nil || name != "办公电脑" {
		t.Fatal("refresh changed metadata", e)
	}
	// Two real SSH streams through the HTTPS bridge; revoke exactly one family.
	secure := httptest.NewTLSServer(a.Handler())
	defer secure.Close()
	c, e := nativeclient.New(secure.URL, secure.Client().Transport)
	if e != nil {
		t.Fatal(e)
	}
	open := func(access string) (*ssh.Client, io.WriteCloser, *bufio.Reader) {
		peer, e := c.ConnectSSH(ctx, nativeclient.Token{Access: access}, asset)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { peer.Close() })
		sh, e := peer.NewSession()
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { sh.Close() })
		in, _ := sh.StdinPipe()
		out, _ := sh.StdoutPipe()
		if e = sh.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); e != nil {
			t.Fatal(e)
		}
		if e = sh.Shell(); e != nil {
			t.Fatal(e)
		}
		reader := bufio.NewReader(out)
		if line, e := reader.ReadString('\n'); e != nil || !strings.Contains(line, "READY") {
			t.Fatal("target not ready", e)
		}
		return peer, in, reader
	}
	peer, _, _ := open(first)
	_, secondInput, secondOutput := open(second)
	expect(t, call(t, srv.URL, "DELETE", "/api/account/native-devices/"+firstID, map[string]string{"password": testPassword}, owner, origin), 200)
	closed := make(chan struct{})
	go func() { peer.Wait(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked device kept its SSH stream")
	}
	if _, e = secondInput.Write([]byte("hostname\n")); e != nil {
		t.Fatal(e)
	}
	for {
		line, e := secondOutput.ReadString('\n')
		if e != nil {
			t.Fatal("other device was disconnected", e)
		}
		if strings.Contains(line, "EXECUTED hostname") {
			break
		}
	}
	if _, e = c.Assets(ctx, nativeclient.Token{Access: first}); e == nil {
		t.Fatal("old access survived revocation")
	}
	if _, e = c.Assets(ctx, nativeclient.Token{Access: renewedToken["access_token"].(string)}); e == nil {
		t.Fatal("rotated access survived revocation")
	}
	expect(t, postToken(renewedToken["refresh_token"].(string)), 400)
	expect(t, call(t, srv.URL, "DELETE", "/api/account/native-devices/"+firstID, map[string]string{"password": testPassword}, owner, origin), 200)
	var count int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM operation_logs WHERE action='identity.native_device_revoked' AND details->>'deviceId'=$1`, firstID).Scan(&count); e != nil || count != 1 {
		t.Fatal("non-idempotent revocation audit", count, e)
	}
	for _, raw := range list(owner) {
		v := raw.(map[string]any)
		if v["id"] == firstID && v["status"] != "revoked" {
			t.Fatal("missing revoked status")
		}
		if v["id"] == secondID && v["status"] != "active" {
			t.Fatal("other device lost authorization")
		}
	}
	// Explicit application-wide revocation must still revoke every device.
	var consent string
	if e = pool.QueryRow(ctx, `SELECT consent_id FROM oauth_token_families WHERE id=$1`, secondID).Scan(&consent); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/account/oauth-consents/"+consent, map[string]string{"password": testPassword}, owner, origin), 200)
	if _, e = c.Assets(ctx, nativeclient.Token{Access: second}); e == nil {
		t.Fatal("application-wide revocation no longer works")
	}
}
