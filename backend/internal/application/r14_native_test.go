package application

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/nativeclient"
	"strings"
	"testing"
)

func TestR14NativeOAuthBoundary(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	uid := postID(t, srv, "/api/admin/users", userInput("native-user", nil, nil), admin)
	user := loginAs(t, srv, "native-user")
	issuer := "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, issuer)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/oauth-server", map[string]any{"issuer": issuer, "nativeEnabled": true, "enabled": false, "version": 1, "password": testPassword}, admin, origin), 200)
	cred := postID(t, srv, "/api/admin/credentials", credInput("native-only"), admin)
	asset := postID(t, srv, "/api/admin/assets", assetInput("Visible", cred, ""), admin)
	postID(t, srv, "/api/admin/assets", assetInput("Hidden", cred, ""), admin)
	grant := postID(t, srv, "/api/admin/grants", map[string]any{"userId": uid, "resourceId": asset}, admin)
	verifier := strings.Repeat("n", 64)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	callback := "http://127.0.0.1:54321/callback"
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start := func(redirect, scope, client string) (int, string) {
		q := url.Values{"client_id": {client}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {scope}, "state": {"native-state"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
		req, _ := http.NewRequest("GET", srv.URL+"/api/oidc/server/authorize?"+q.Encode(), nil)
		req.AddCookie(user)
		res, e := noRedirect.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		return res.StatusCode, res.Header.Get("Location")
	}
	code := func(scope string) string {
		status, location := start(callback, scope, identity.NativeClientID)
		if status != 303 {
			t.Fatal("native authorize", status)
		}
		u, _ := url.Parse(location)
		r := call(t, srv.URL, "POST", "/api/oidc/server/consent", map[string]any{"request": u.Query().Get("request"), "approve": true}, user, origin)
		expect(t, r, 200)
		redirect, _ := url.Parse(object(t, r)["redirectUrl"].(string))
		if redirect.Query().Get("state") != "native-state" {
			t.Fatal("state")
		}
		return redirect.Query().Get("code")
	}
	post := func(path string, values url.Values) result {
		values.Set("client_id", identity.NativeClientID)
		req, _ := http.NewRequest("POST", srv.URL+"/api/oidc/server/"+path, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return result{status: res.StatusCode, body: b}
	}
	exchange := func(code, proof, redirect string) result {
		return post("token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {proof}, "redirect_uri": {redirect}})
	}
	bearer := func(path, token string) result {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return result{status: res.StatusCode, body: b}
	}
	for _, redirect := range []string{"http://localhost:54321/callback", "https://evil.example/callback", "http://127.0.0.1:54321/other", "http://127.0.0.1:54321/callback?x=y"} {
		if status, loc := start(redirect, "assets:read", identity.NativeClientID); status != 400 || loc != "" {
			t.Fatal("unsafe redirect accepted")
		}
	}
	if status, _ := start(callback, "openid", identity.NativeClientID); status != 400 {
		t.Fatal("native scope escalation")
	}
	if status, _ := start(callback, "assets:read", identity.NativeClientUUID); status != 400 {
		t.Fatal("internal client id exposed")
	}
	bad := code("assets:read")
	expect(t, exchange(bad, strings.Repeat("x", 64), callback), 400)
	expect(t, exchange(bad, verifier, callback), 400)
	wrongPort := code("assets:read")
	expect(t, exchange(wrongPort, verifier, "http://127.0.0.1:54322/callback"), 400)
	good := code("assets:read sessions:connect offline_access")
	r := exchange(good, verifier, callback)
	expect(t, r, 200)
	tokens := object(t, r)
	if _, ok := tokens["id_token"]; ok {
		t.Fatal("native client got OIDC token")
	}
	access, refresh := tokens["access_token"].(string), tokens["refresh_token"].(string)
	expect(t, exchange(good, verifier, callback), 400)
	r = bearer("/api/native/v1/assets?limit=1", access)
	expect(t, r, 200)
	if len(object(t, r)["items"].([]any)) != 1 || strings.Contains(string(r.body), "192.0.2.10") || strings.Contains(string(r.body), cred) || strings.Contains(string(r.body), "Hidden") {
		t.Fatal("asset disclosure", string(r.body))
	}
	expect(t, bearer("/api/native/v1/assets", refresh), 401)
	expect(t, bearer("/api/account/info", access), 401)
	expect(t, bearer("/api/oidc/server/userinfo", access), 401)
	expect(t, call(t, srv.URL, "GET", "/api/native/v1/assets", nil, user, origin), 401)
	expect(t, post("introspect", url.Values{"token": {access}}), 403)
	expect(t, post("token", url.Values{"grant_type": {"client_credentials"}, "scope": {"service"}}), 400)
	var ttlOK bool
	if e := pool.QueryRow(context.Background(), `SELECT expires_at BETWEEN now()+interval '6 days' AND now()+interval '7 days 1 minute' FROM oauth_token_families WHERE client_id=$1`, identity.NativeClientUUID).Scan(&ttlOK); e != nil || !ttlOK {
		t.Fatal("native refresh TTL", e)
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/grants/"+grant, nil, admin, origin), 204)
	r = bearer("/api/native/v1/assets", access)
	expect(t, r, 200)
	if len(object(t, r)["items"].([]any)) != 0 {
		t.Fatal("revoked asset still visible")
	}
	rotated := post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
	expect(t, rotated, 200)
	expect(t, post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}), 400)
	expect(t, bearer("/api/native/v1/assets", object(t, rotated)["access_token"].(string)), 401)
	limited := exchange(code("sessions:connect"), verifier, callback)
	expect(t, limited, 200)
	expect(t, bearer("/api/native/v1/assets", object(t, limited)["access_token"].(string)), 403)
	latest := exchange(code("assets:read"), verifier, callback)
	expect(t, latest, 200)
	access = object(t, latest)["access_token"].(string)
	expect(t, post("revoke", url.Values{"token": {access}}), 200)
	expect(t, bearer("/api/native/v1/assets", access), 401)
	latest = exchange(code("assets:read"), verifier, callback)
	expect(t, latest, 200)
	access = object(t, latest)["access_token"].(string)
	currentServer, err := a.identity.OAuthSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/oauth-server", map[string]any{"issuer": issuer, "nativeEnabled": false, "enabled": false, "version": currentServer.Version, "password": testPassword}, admin, origin), 200)
	expect(t, bearer("/api/native/v1/assets", access), 401)
	if status, _ := start(callback, "assets:read", identity.NativeClientID); status != 503 {
		t.Fatal("native switch ignored")
	}
}

// Runs the actual client against the actual TLS API and loopback listener. The
// browser callback is exercised with an existing authenticated fixture session.
func TestR14NativeClientEndToEnd(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	secure := httptest.NewTLSServer(a.Handler())
	defer secure.Close()
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, secure.URL)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/oauth-server", map[string]any{"issuer": secure.URL, "nativeEnabled": true, "version": 1, "password": testPassword}, admin, origin), 200)
	cred := postID(t, srv, "/api/admin/credentials", credInput("CLI credential"), admin)
	postID(t, srv, "/api/admin/assets", assetInput("CLI visible asset", cred, ""), admin)
	client, e := nativeclient.New(secure.URL, secure.Client().Transport)
	if e != nil {
		t.Fatal(e)
	}
	browser := secure.Client()
	client.SetDeviceName("CLI 验收电脑")
	browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	open := func(address string) error {
		auth, _ := url.Parse(address)
		callback, _ := url.Parse(auth.Query().Get("redirect_uri"))
		bad := *callback
		q := bad.Query()
		q.Set("state", "wrong")
		q.Set("code", strings.Repeat("x", 64))
		bad.RawQuery = q.Encode()
		response, e := http.Get(bad.String())
		if e != nil {
			return e
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatal("loopback accepted wrong state")
		}
		req, _ := http.NewRequest("GET", address, nil)
		req.AddCookie(admin)
		response, e = browser.Do(req)
		if e != nil {
			return e
		}
		response.Body.Close()
		if response.StatusCode != 303 {
			t.Fatal("authorize", response.StatusCode)
		}
		location, _ := url.Parse(response.Header.Get("Location"))
		consent := call(t, srv.URL, "POST", "/api/oidc/server/consent", map[string]any{"request": location.Query().Get("request"), "approve": true}, admin, origin)
		expect(t, consent, 200)
		response, e = http.Get(object(t, consent)["redirectUrl"].(string))
		if e != nil {
			return e
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("loopback callback", response.StatusCode)
		}
		return nil
	}
	token, e := client.Login(context.Background(), open)
	if e != nil {
		t.Fatal(e)
	}
	assets, e := client.Assets(context.Background(), token)
	if e != nil || len(assets) != 1 || assets[0].Name != "CLI visible asset" {
		t.Fatal("client directory", assets, e)
	}
	devices := call(t, srv.URL, "GET", "/api/account/native-devices", nil, admin, origin)
	expect(t, devices, 200)
	rows := object(t, devices)["items"].([]any)
	if len(rows) != 1 || token.DeviceID == "" || rows[0].(map[string]any)["id"] != token.DeviceID || rows[0].(map[string]any)["name"] != "CLI 验收电脑" || rows[0].(map[string]any)["os"] == "unknown" {
		t.Fatal("client login did not register its device")
	}
	if e = client.Logout(context.Background(), token); e != nil {
		t.Fatal(e)
	}
	if _, e = client.Assets(context.Background(), token); e == nil {
		t.Fatal("logged-out client token survived")
	}
	t.Run("remember-and-rotate", func(t *testing.T) {
		store := &r14MemoryCredential{}
		if e := client.RememberLogin(context.Background(), store, open, true); e != nil {
			t.Fatal(e)
		}
		first, e := client.RememberedToken(context.Background(), store, true)
		if e != nil {
			t.Fatal(e)
		}
		second, e := client.RememberedToken(context.Background(), store, true)
		if e != nil {
			t.Fatal(e)
		}
		if first.DeviceID != second.DeviceID || first.Refresh == second.Refresh || first.Access == second.Access {
			t.Fatal("rotation changed device or reused tokens")
		}
		if items, e := client.Assets(context.Background(), first); e != nil || len(items) != 1 {
			t.Fatal("previous short-lived access token invalidated by ordinary refresh", e)
		}
		if items, e := client.Assets(context.Background(), second); e != nil || len(items) != 1 {
			t.Fatal("refreshed directory", e)
		}
		if e := client.ForgetLogin(context.Background(), store); e != nil {
			t.Fatal(e)
		}
		if store.data != nil {
			t.Fatal("logout retained credential")
		}
		if _, e := client.Assets(context.Background(), first); e == nil {
			t.Fatal("first token survived logout")
		}
		if _, e := client.Assets(context.Background(), second); e == nil {
			t.Fatal("second token survived logout")
		}
		if e := client.RememberLogin(context.Background(), store, open, false); e != nil {
			t.Fatal(e)
		}
		third, e := client.RememberedToken(context.Background(), store, false)
		if e != nil {
			t.Fatal(e)
		}
		expect(t, call(t, srv.URL, "DELETE", "/api/account/native-devices/"+third.DeviceID, map[string]any{"password": testPassword}, admin, origin), 200)
		if _, e := client.RememberedToken(context.Background(), store, false); e == nil {
			t.Fatal("revoked saved device refreshed")
		}
		if store.data != nil {
			t.Fatal("revoked refresh credential not cleared")
		}
		failedStore := &r14MemoryCredential{failWrites: true}
		if e := client.RememberLogin(context.Background(), failedStore, open, false); e == nil {
			t.Fatal("accepted failed initial credential save")
		}
		allDevices := call(t, srv.URL, "GET", "/api/account/native-devices", nil, admin, origin)
		expect(t, allDevices, 200)
		for _, row := range object(t, allDevices)["items"].([]any) {
			if row.(map[string]any)["status"] != "revoked" {
				t.Fatal("initial save failure left an active device")
			}
		}
	})
}
