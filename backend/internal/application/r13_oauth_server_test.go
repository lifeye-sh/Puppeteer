package application

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"github.com/coreos/go-oidc/v3/oidc"
	"io"
	"net/http"
	"net/url"
	"next-terminal/backend/internal/modules/identity"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestR13OAuthServerLifecycle(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	const issuer = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, issuer)
	admin := loginAs(t, srv, "accept-admin")
	serverVersion := func() int64 {
		t.Helper()
		v, e := a.identity.OAuthSettings(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		return v.Version
	}
	configuration, e := a.system.Configuration(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	settingsResponse, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(configuration.ID), map[string]any{"set": map[string]any{"oidc-server-access-token-ttl": 120, "oidc-server-refresh-token-ttl": 3600, "oidc-server-id-token-ttl": 180, "oidc-server-auth-code-ttl": 45}, "password": testPassword}, admin)
	expect(t, settingsResponse, 202)
	uid := postID(t, srv, "/api/admin/users", userInput("oauth-user", nil, nil), admin)
	user := loginAs(t, srv, "oauth-user")
	postID(t, srv, "/api/admin/users", userInput("oauth-other", nil, nil), admin)
	other := loginAs(t, srv, "oauth-other")
	expect(t, call(t, srv.URL, "PUT", "/api/admin/oauth-server", map[string]any{"issuer": issuer, "enabled": true, "version": serverVersion(), "password": testPassword}, admin, origin), 200)
	cfg := map[string]any{"name": "isolated relying party", "redirectUris": []string{"https://app.example/callback"}, "scopes": []string{"openid", "profile", "offline_access", "service"}, "allowedUsers": []string{uid}, "enabled": true, "clientCredentials": true, "password": testPassword}
	created := call(t, srv.URL, "POST", "/api/admin/oauth-clients", cfg, admin, origin)
	expect(t, created, 200)
	client := object(t, created)["id"].(string)
	secret := object(t, created)["secret"].(string)
	if len(secret) != 64 {
		t.Fatal("client secret")
	}
	listed := call(t, srv.URL, "GET", "/api/admin/oauth-clients", nil, admin, origin)
	expect(t, listed, 200)
	if strings.Contains(string(listed.body), secret) {
		t.Fatal("secret disclosed")
	}
	discovery := call(t, srv.URL, "GET", "/.well-known/openid-configuration", nil, nil, "")
	expect(t, discovery, 200)
	if object(t, discovery)["issuer"] != issuer {
		t.Fatal("issuer not fixed")
	}
	verifier := strings.Repeat("a", 64)
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	authorization := url.Values{"client_id": {client}, "redirect_uri": {"https://app.example/callback"}, "response_type": {"code"}, "scope": {"openid profile offline_access"}, "state": {"client-state"}, "nonce": {"client-nonce"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start := func(c *http.Cookie, q url.Values) (int, string) {
		req, _ := http.NewRequest("GET", srv.URL+"/api/oidc/server/authorize?"+q.Encode(), nil)
		if c != nil {
			req.AddCookie(c)
		}
		res, e := noRedirect.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		return res.StatusCode, res.Header.Get("Location")
	}
	status, _ := start(other, authorization)
	if status != 403 {
		t.Fatal("user boundary", status)
	}
	bad := url.Values{}
	for k, v := range authorization {
		bad[k] = append([]string{}, v...)
	}
	bad.Set("redirect_uri", "https://evil.example")
	status, location := start(user, bad)
	if status != 400 || location != "" {
		t.Fatal("redirect boundary")
	}
	newCode := func() string {
		status, location := start(user, authorization)
		if status != 303 {
			t.Fatal("authorize", status, location)
		}
		u, _ := url.Parse(location)
		pending := u.Query().Get("request")
		expect(t, call(t, srv.URL, "GET", "/api/oidc/server/consent?request="+pending, nil, other, origin), 400)
		r := call(t, srv.URL, "POST", "/api/oidc/server/consent", map[string]any{"request": pending, "approve": true}, user, origin)
		expect(t, r, 200)
		cb, _ := url.Parse(object(t, r)["redirectUrl"].(string))
		if cb.Query().Get("state") != "client-state" {
			t.Fatal("state")
		}
		expect(t, call(t, srv.URL, "POST", "/api/oidc/server/consent", map[string]any{"request": pending, "approve": true}, user, origin), 400)
		return cb.Query().Get("code")
	}
	post := func(path string, values url.Values) result {
		a.mu.Lock()
		a.attempts = map[string]attempt{}
		a.mu.Unlock()
		req, _ := http.NewRequest("POST", srv.URL+"/api/oidc/server/"+path, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth(client, secret)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return result{status: res.StatusCode, body: body}
	}
	info := func(token string) result {
		req, _ := http.NewRequest("GET", srv.URL+"/api/oidc/server/userinfo", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return result{status: res.StatusCode, body: body}
	}
	exchange := func(code, proof string) result {
		return post("token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"https://app.example/callback"}, "code_verifier": {proof}})
	}
	code := newCode()
	var codeRemaining float64
	if e := pool.QueryRow(context.Background(), `SELECT extract(epoch FROM(expires_at-now())) FROM oauth_codes WHERE token_hash=$1`, identity.Digest(code)).Scan(&codeRemaining); e != nil || codeRemaining > 45 || codeRemaining < 35 {
		t.Fatal("configured code expiry", codeRemaining, e)
	}
	r := exchange(code, verifier)
	expect(t, r, 200)
	tokens := object(t, r)
	if tokens["expires_in"] != float64(120) {
		t.Fatal("configured access expiry", tokens["expires_in"])
	}
	access := tokens["access_token"].(string)
	refresh := tokens["refresh_token"].(string)
	expect(t, exchange(code, verifier), 400)
	expect(t, info(access), 200)
	keyset := oidc.NewRemoteKeySet(context.Background(), srv.URL+"/api/oidc/server/jwks")
	verified, e := oidc.NewVerifier(issuer, keyset, &oidc.Config{ClientID: client, SupportedSigningAlgs: []string{"RS256"}}).Verify(context.Background(), tokens["id_token"].(string))
	if e != nil || verified.Subject != uid || verified.Nonce != "client-nonce" {
		t.Fatal("real OIDC verifier", verified, e)
	}
	if verified.Expiry.Sub(verified.IssuedAt) != 180*time.Second {
		t.Fatal("configured ID expiry", verified.Expiry, verified.IssuedAt)
	}
	var familyRemaining float64
	if e := pool.QueryRow(context.Background(), `SELECT extract(epoch FROM(f.expires_at-now())) FROM oauth_token_families f JOIN oauth_tokens t ON t.family_id=f.id WHERE t.token_hash=$1`, identity.Digest(refresh)).Scan(&familyRemaining); e != nil || familyRemaining > 3600 || familyRemaining < 3580 {
		t.Fatal("configured refresh family", familyRemaining, e)
	}
	if e = verified.VerifyAccessToken(access); e != nil {
		t.Fatal("access hash", e)
	}
	r = post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
	expect(t, r, 200)
	rotated := object(t, r)
	expect(t, post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}), 400)
	expect(t, info(rotated["access_token"].(string)), 401)
	expect(t, post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rotated["refresh_token"].(string)}}), 400)
	code = newCode()
	expect(t, exchange(code, strings.Repeat("b", 64)), 400)
	expect(t, exchange(code, verifier), 400)
	code = newCode()
	r = exchange(code, verifier)
	expect(t, r, 200)
	refresh = object(t, r)["refresh_token"].(string)
	var wg sync.WaitGroup
	responses := make(chan result, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses <- post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
		}()
	}
	wg.Wait()
	close(responses)
	success := 0
	for r := range responses {
		if r.status == 200 {
			success++
			access = object(t, r)["access_token"].(string)
		} else if r.status != 400 {
			t.Fatal("refresh race", r.status)
		}
	}
	if success != 1 {
		t.Fatal("refresh reused", success)
	}
	expect(t, info(access), 401)
	r = post("token", url.Values{"grant_type": {"client_credentials"}, "scope": {"service"}})
	expect(t, r, 200)
	service := object(t, r)["access_token"].(string)
	expect(t, info(service), 401)
	r = post("introspect", url.Values{"token": {service}})
	expect(t, r, 200)
	if object(t, r)["active"] != true {
		t.Fatal("introspect")
	}
	expect(t, post("revoke", url.Values{"token": {service}}), 200)
	r = post("introspect", url.Values{"token": {service}})
	if object(t, r)["active"] != false {
		t.Fatal("revoke")
	}
	code = newCode()
	r = exchange(code, verifier)
	expect(t, r, 200)
	tokens = object(t, r)
	var consent string
	if e = pool.QueryRow(context.Background(), `SELECT id FROM oauth_consents WHERE user_id=$1 AND client_id=$2`, uid, client).Scan(&consent); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/account/oauth-consents/"+consent, map[string]string{"password": testPassword}, other, origin), 403)
	expect(t, call(t, srv.URL, "DELETE", "/api/account/oauth-consents/"+consent, map[string]string{"password": testPassword}, user, origin), 200)
	expect(t, info(tokens["access_token"].(string)), 401)
	expect(t, post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens["refresh_token"].(string)}}), 400)
	resetRate := func() { a.mu.Lock(); a.attempts = map[string]attempt{}; a.mu.Unlock() }
	t.Run("request-boundaries", func(t *testing.T) {
		for _, change := range []struct{ key, value string }{{"nonce", ""}, {"code_challenge_method", "plain"}, {"scope", "openid admin"}, {"request_uri", "https://attacker.example/request"}, {"response_type", "token"}} {
			q := url.Values{}
			for k, v := range authorization {
				q[k] = append([]string{}, v...)
			}
			q.Set(change.key, change.value)
			status, _ := start(user, q)
			if status != 400 {
				t.Fatal(change.key, status)
			}
		}
		q := url.Values{}
		for k, v := range authorization {
			q[k] = append([]string{}, v...)
		}
		q.Set("prompt", "none")
		status, location := start(user, q)
		target, _ := url.Parse(location)
		if status != 303 || target.Query().Get("error") != "consent_required" {
			t.Fatal("silent consent", status)
		}
		status, location = start(user, authorization)
		target, _ = url.Parse(location)
		request := target.Query().Get("request")
		denied := call(t, srv.URL, "POST", "/api/oidc/server/consent", map[string]any{"request": request, "approve": false}, user, origin)
		expect(t, denied, 200)
		target, _ = url.Parse(object(t, denied)["redirectUrl"].(string))
		if target.Query().Get("error") != "access_denied" {
			t.Fatal("consent refusal")
		}
	})
	t.Run("expired-code-and-access", func(t *testing.T) {
		code := newCode()
		pool.Exec(context.Background(), `UPDATE oauth_codes SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, identity.Digest(code))
		expect(t, exchange(code, verifier), 400)
		issued := exchange(newCode(), verifier)
		expect(t, issued, 200)
		access := object(t, issued)["access_token"].(string)
		pool.Exec(context.Background(), `UPDATE oauth_tokens SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, identity.Digest(access))
		expect(t, info(access), 401)
	})
	t.Run("refresh-downscope-is-permanent", func(t *testing.T) {
		issued := exchange(newCode(), verifier)
		expect(t, issued, 200)
		refresh := object(t, issued)["refresh_token"].(string)
		narrow := post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "scope": {"openid offline_access"}})
		expect(t, narrow, 200)
		data := object(t, narrow)
		if object(t, info(data["access_token"].(string)))["name"] != nil {
			t.Fatal("profile leaked after downscope")
		}
		rotated := post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {data["refresh_token"].(string)}})
		expect(t, rotated, 200)
		data = object(t, rotated)
		if data["scope"] != "openid offline_access" {
			t.Fatal("scope expanded on refresh")
		}
		expect(t, post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {data["refresh_token"].(string)}, "scope": {"openid profile offline_access"}}), 400)
	})
	t.Run("key-rotation-keeps-valid-grants", func(t *testing.T) {
		issued := exchange(newCode(), verifier)
		expect(t, issued, 200)
		data := object(t, issued)
		resetRate()
		expect(t, call(t, srv.URL, "PUT", "/api/admin/oauth-server", map[string]any{"issuer": issuer, "enabled": true, "version": serverVersion(), "rotateKey": true, "password": testPassword}, admin, origin), 200)
		var grace float64
		if e := pool.QueryRow(context.Background(), `SELECT min(extract(epoch FROM(retire_at-now()))) FROM oauth_signing_keys WHERE NOT active AND retire_at>now()`).Scan(&grace); e != nil || grace < 3600 {
			t.Fatal("rotation fails maximum ID lifetime", grace, e)
		}
		keys := object(t, call(t, srv.URL, "GET", "/api/oidc/server/jwks", nil, nil, ""))["keys"].([]any)
		if len(keys) != 2 {
			t.Fatal("rotation grace keys", len(keys))
		}
		verifierOIDC := oidc.NewVerifier(issuer, oidc.NewRemoteKeySet(context.Background(), srv.URL+"/api/oidc/server/jwks"), &oidc.Config{ClientID: client, SupportedSigningAlgs: []string{"RS256"}})
		if _, e := verifierOIDC.Verify(context.Background(), data["id_token"].(string)); e != nil {
			t.Fatal(e)
		}
		expect(t, info(data["access_token"].(string)), 200)
		expect(t, post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {data["refresh_token"].(string)}}), 200)
	})
	t.Run("hmac-rotation-and-tampering", func(t *testing.T) {
		ctx := context.Background()
		rotate := func() {
			t.Helper()
			resetRate()
			v, e := a.system.Configuration(ctx, 0)
			if e != nil {
				t.Fatal(e)
			}
			r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"clearSecrets": []string{"oidc-server-hmac-secret"}, "password": testPassword}, admin)
			expect(t, r, 202)
		}
		pending := func() string {
			t.Helper()
			status, location := start(user, authorization)
			if status != 303 {
				t.Fatal("pending", status)
			}
			u, _ := url.Parse(location)
			return u.Query().Get("request")
		}
		request := pending()
		rotate()
		approved := call(t, srv.URL, "POST", "/api/oidc/server/consent", map[string]any{"request": request, "approve": true}, user, origin)
		expect(t, approved, 200)
		cb, _ := url.Parse(object(t, approved)["redirectUrl"].(string))
		code := cb.Query().Get("code")
		rotate()
		issued := exchange(code, verifier)
		expect(t, issued, 200)
		data := object(t, issued)
		access := data["access_token"].(string)
		refresh := data["refresh_token"].(string)
		var saved []byte
		if e := pool.QueryRow(ctx, `SELECT hmac_tag FROM oauth_tokens WHERE token_hash=$1`, identity.Digest(refresh)).Scan(&saved); e != nil {
			t.Fatal(e)
		}
		// An access MAC cannot authenticate a refresh token (or another token).
		if _, e := pool.Exec(ctx, `UPDATE oauth_tokens SET hmac_tag=(SELECT hmac_tag FROM oauth_tokens WHERE token_hash=$1) WHERE token_hash=$2`, identity.Digest(access), identity.Digest(refresh)); e != nil {
			t.Fatal(e)
		}
		expect(t, post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}), 400)
		if _, e := pool.Exec(ctx, `UPDATE oauth_tokens SET hmac_tag=$2 WHERE token_hash=$1`, identity.Digest(refresh), saved); e != nil {
			t.Fatal(e)
		}
		rotate()
		expect(t, info(access), 200)
		expect(t, post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}), 200)
		request = pending()
		if _, e := pool.Exec(ctx, `UPDATE oauth_pending_authorizations SET hmac_tag=$2 WHERE token_hash=$1`, identity.Digest(request), make([]byte, 32)); e != nil {
			t.Fatal(e)
		}
		expect(t, call(t, srv.URL, "GET", "/api/oidc/server/consent?request="+request, nil, user, origin), 400)
		code = newCode()
		if _, e := pool.Exec(ctx, `UPDATE oauth_codes SET hmac_tag=$2 WHERE token_hash=$1`, identity.Digest(code), make([]byte, 32)); e != nil {
			t.Fatal(e)
		}
		expect(t, exchange(code, verifier), 400)
	})
	t.Run("department-and-verified-claims", func(t *testing.T) {
		resetRate()
		department := postID(t, srv, "/api/admin/departments", map[string]string{"name": "OAuth mapped department"}, admin)
		pool.Exec(context.Background(), `INSERT INTO user_departments(user_id,department_id) VALUES($1,$2)`, uid, department)
		pool.Exec(context.Background(), `INSERT INTO verified_contacts(user_id,kind,address) VALUES($1,'email','oauth@example.test'),($1,'phone','+8613800000001')`, uid)
		cfg["version"] = 1
		cfg["allowedUsers"] = []string{}
		cfg["allowedDepartments"] = []string{department}
		cfg["scopes"] = []string{"openid", "profile", "email", "phone", "groups", "offline_access", "service"}
		cfg["claimMapping"] = map[string]string{"name": "username", "groups": "department_names"}
		expect(t, call(t, srv.URL, "PUT", "/api/admin/oauth-clients/"+client, cfg, admin, origin), 200)
		authorization.Set("scope", "openid profile email phone groups offline_access")
		issued := exchange(newCode(), verifier)
		expect(t, issued, 200)
		data := object(t, issued)
		claims := object(t, info(data["access_token"].(string)))
		if claims["name"] != "oauth-user" || claims["email_verified"] != true || claims["phone_number_verified"] != true || claims["groups"].([]any)[0] != "OAuth mapped department" {
			t.Fatal("mapped or verified claims missing")
		}
		pool.Exec(context.Background(), `DELETE FROM user_departments WHERE user_id=$1 AND department_id=$2`, uid, department)
		expect(t, info(data["access_token"].(string)), 401)
		expect(t, post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {data["refresh_token"].(string)}}), 400)
		pool.Exec(context.Background(), `INSERT INTO user_departments(user_id,department_id) VALUES($1,$2)`, uid, department)
	})
	t.Run("client-disable-and-secret-rotation", func(t *testing.T) {
		issued := exchange(newCode(), verifier)
		expect(t, issued, 200)
		data := object(t, issued)
		cfg["version"] = 2
		cfg["enabled"] = false
		resetRate()
		expect(t, call(t, srv.URL, "PUT", "/api/admin/oauth-clients/"+client, cfg, admin, origin), 200)
		expect(t, info(data["access_token"].(string)), 401)
		expect(t, post("token", url.Values{"grant_type": {"client_credentials"}, "scope": {"service"}}), 401)
		cfg["version"] = 3
		cfg["enabled"] = true
		expect(t, call(t, srv.URL, "PUT", "/api/admin/oauth-clients/"+client, cfg, admin, origin), 200)
		expect(t, info(data["access_token"].(string)), 401)
		issued = exchange(newCode(), verifier)
		expect(t, issued, 200)
		data = object(t, issued)
		cfg["version"] = 4
		cfg["rotateSecret"] = true
		rotated := call(t, srv.URL, "PUT", "/api/admin/oauth-clients/"+client, cfg, admin, origin)
		expect(t, rotated, 200)
		expect(t, post("token", url.Values{"grant_type": {"client_credentials"}, "scope": {"service"}}), 401)
		secret = object(t, rotated)["secret"].(string)
		expect(t, post("token", url.Values{"grant_type": {"client_credentials"}, "scope": {"service"}}), 200)
		expect(t, info(data["access_token"].(string)), 401)
	})
	t.Run("server-disable-does-not-revive-grants", func(t *testing.T) {
		issued := exchange(newCode(), verifier)
		expect(t, issued, 200)
		data := object(t, issued)
		resetRate()
		expect(t, call(t, srv.URL, "PUT", "/api/admin/oauth-server", map[string]any{"issuer": issuer, "enabled": false, "version": serverVersion(), "password": testPassword}, admin, origin), 200)
		expect(t, info(data["access_token"].(string)), 401)
		expect(t, call(t, srv.URL, "PUT", "/api/admin/oauth-server", map[string]any{"issuer": issuer, "enabled": true, "version": serverVersion(), "password": testPassword}, admin, origin), 200)
		expect(t, info(data["access_token"].(string)), 401)
		expect(t, post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {data["refresh_token"].(string)}}), 400)
	})
	t.Run("user-reset-and-pending-mfa", func(t *testing.T) {
		issued := exchange(newCode(), verifier)
		expect(t, issued, 200)
		data := object(t, issued)
		if e := a.db.Within(context.Background(), func(ctx context.Context) error { return a.identity.RevokeAll(ctx, uid) }); e != nil {
			t.Fatal(e)
		}
		expect(t, info(data["access_token"].(string)), 401)
		expect(t, post("token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {data["refresh_token"].(string)}}), 400)
		resetRate()
		user = loginAs(t, srv, "oauth-user")
		_, _, user = enroll(t, srv, user)
		pending := call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "oauth-user", "password": testPassword}, nil, origin)
		expect(t, pending, 200)
		status, location := start(pending.cookie, authorization)
		if status != 303 || !strings.HasPrefix(location, "/login?") {
			t.Fatal("MFA-pending authorized OIDC", status, location)
		}
		if e := a.db.Within(context.Background(), func(ctx context.Context) error { return a.identity.QuarantineRestore(ctx) }); e != nil {
			t.Fatal(e)
		}
		expect(t, info(data["access_token"].(string)), 401)
	})

}
