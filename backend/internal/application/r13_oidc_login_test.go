package application

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"next-terminal/backend/internal/modules/identity"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestR13OIDCLoginLifecycle(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	const secureOrigin = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, secureOrigin)
	admin := loginAs(t, srv, "accept-admin")
	changeLoginDuration(t, srv, admin, 7)
	uid := postID(t, srv, "/api/admin/users", userInput("oidc-login-user", nil, nil), admin)
	owner := loginAs(t, srv, "oidc-login-user")
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	var issuer, nonce, challenge string
	subject := "login-user"
	idp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(identity.OIDCDiscovery{Issuer: issuer, AuthorizationEndpoint: issuer + "/authorize", TokenEndpoint: issuer + "/token", JWKSURI: issuer + "/keys", ResponseTypes: []string{"code"}, SigningAlgorithms: []string{"RS256"}, PKCEMethods: []string{"S256"}})
		case "/token":
			r.ParseForm()
			client, secret, ok := r.BasicAuth()
			if !ok || client != "fixture-client" || secret != "fixture-secret" || oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != challenge {
				t.Error("invalid exchange proof")
			}
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": issuer, "sub": subject, "aud": "fixture-client", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "nonce": nonce})
			token.Header["kid"] = "fixture"
			raw, err := token.SignedString(key)
			if err != nil {
				t.Error(err)
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": "fixture", "token_type": "Bearer", "id_token": raw})
		case "/keys":
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "fixture", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer idp.Close()
	issuer = idp.URL
	a.oidcHTTPClient = idp.Client()
	in := map[string]any{"name": "login fixture", "issuer": issuer, "clientId": "fixture-client", "clientSecret": "fixture-secret", "redirectUri": secureOrigin + "/api/oidc/callback", "enabled": true, "password": testPassword}
	pid := postID(t, srv, "/api/admin/oidc-providers", in, admin)
	var eid string
	if e = pool.QueryRow(ctx, `INSERT INTO external_identities(user_id,provider_id,issuer,subject) VALUES($1,$2,$3,$4) RETURNING id`, uid, pid, issuer, subject).Scan(&eid); e != nil {
		t.Fatal(e)
	}
	path := "/api/oidc/login/" + pid + "/start"
	start := func() (string, *http.Cookie) {
		a.mu.Lock()
		a.attempts = map[string]attempt{}
		a.mu.Unlock()
		r := call(t, srv.URL, "POST", path, map[string]string{}, nil, secureOrigin)
		expect(t, r, 200)
		if r.cookie == nil || r.cookie.Name != oidcLoginCookie || !r.cookie.Secure || !r.cookie.HttpOnly || r.cookie.SameSite != http.SameSiteLaxMode || r.cookie.Path != "/" {
			t.Fatal("unsafe login binding cookie")
		}
		u, err := url.Parse(object(t, r)["authorizationUrl"].(string))
		if err != nil {
			t.Fatal(err)
		}
		nonce = u.Query().Get("nonce")
		challenge = u.Query().Get("code_challenge")
		return "/api/oidc/callback?" + url.Values{"state": {u.Query().Get("state")}, "code": {"fixture-code"}}.Encode(), r.cookie
	}
	finish := func(path string, cookie *http.Cookie) *http.Cookie {
		req, err := http.NewRequest("GET", srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		for _, c := range response.Cookies() {
			if c.Name == CookieName && c.Value != "" {
				if !c.Secure || !c.HttpOnly {
					t.Fatal("unsafe session cookie")
				}
				return c
			}
		}
		return nil
	}
	callback, binding := start()
	wrong := *binding
	wrong.Value = identity.NewToken()
	if finish(callback, &wrong) != nil {
		t.Fatal("cross-browser login")
	}
	logged := finish(callback, binding)
	assertCookieDuration(t, logged, 7*60)
	if logged == nil {
		t.Fatal("bound login rejected")
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, logged, secureOrigin), 200)
	if finish(callback, binding) != nil {
		t.Fatal("login replay")
	}
	subject = "unknown-user"
	callback, binding = start()
	if finish(callback, binding) != nil {
		t.Fatal("unbound identity logged in")
	}
	subject = "login-user"
	a.config.CaptchaRequired = true
	expect(t, call(t, srv.URL, "POST", path, map[string]string{}, nil, secureOrigin), 400)
	a.config.CaptchaRequired = false
	callback, binding = start()
	in["version"] = 1
	in["enabled"] = false
	expect(t, call(t, srv.URL, "PUT", "/api/admin/oidc-providers/"+pid, in, admin, origin), 200)
	if finish(callback, binding) != nil {
		t.Fatal("disabled provider login")
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, logged, secureOrigin), 401)
	in["version"] = 2
	in["enabled"] = true
	expect(t, call(t, srv.URL, "PUT", "/api/admin/oidc-providers/"+pid, in, admin, origin), 200)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, logged, secureOrigin), 401)
	secret, _, _ := enroll(t, srv, owner)
	callback, binding = start()
	pending := finish(callback, binding)
	if pending == nil {
		t.Fatal("MFA session missing")
	}
	status := object(t, call(t, srv.URL, "GET", "/api/login-status", nil, pending, secureOrigin))
	if status["status"] != "OTP Required" {
		t.Fatal("MFA bypass", status)
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, pending, secureOrigin), 401)
	upgraded := call(t, srv.URL, "POST", "/api/validate-totp", map[string]string{"totp": identity.TOTP(secret, time.Now().Unix()/30+1)}, pending, secureOrigin)
	expect(t, upgraded, 200)
	if upgraded.cookie == nil || !upgraded.cookie.Secure {
		t.Fatal("MFA upgrade lost Secure cookie")
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, upgraded.cookie, secureOrigin), 200)
	callback, binding = start()
	e = a.db.Within(ctx, func(ctx context.Context) error {
		if err := a.identity.LockUser(ctx, uid); err != nil {
			return err
		}
		return a.identity.UnbindExternalIdentity(ctx, uid, eid)
	})
	if e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, upgraded.cookie, secureOrigin), 401)
	if finish(callback, binding) != nil {
		t.Fatal("unbound identity login")
	}
	// A new binding must not revive a callback initiated before that binding existed.
	callback, binding = start()
	if _, e = pool.Exec(ctx, `INSERT INTO external_identities(user_id,provider_id,issuer,subject) VALUES($1,$2,$3,$4)`, uid, pid, issuer, subject); e != nil {
		t.Fatal(e)
	}
	if finish(callback, binding) != nil {
		t.Fatal("new binding accepted old login challenge")
	}
	callback, binding = start()
	if e = a.identity.RevokeAll(ctx, uid); e != nil {
		t.Fatal(e)
	}
	if finish(callback, binding) != nil {
		t.Fatal("reset accepted old login challenge")
	}
	callback, binding = start()
	if _, e = pool.Exec(ctx, `UPDATE users SET status='disabled' WHERE id=$1`, uid); e != nil {
		t.Fatal(e)
	}
	if finish(callback, binding) != nil {
		t.Fatal("disabled user login")
	}
	if _, e = pool.Exec(ctx, `UPDATE users SET status='enabled' WHERE id=$1`, uid); e != nil {
		t.Fatal(e)
	}

	callback, binding = start()
	for i := 0; i < 5; i++ {
		if e = a.identity.FailedPassword(ctx, "oidc-login-user"); e != nil {
			t.Fatal(e)
		}
	}
	if finish(callback, binding) != nil {
		t.Fatal("locked user login")
	}
	if e = a.identity.Unlock(ctx, "oidc-login-user"); e != nil {
		t.Fatal(e)
	}
	// External-only users can complete a forced enrollment without inventing a
	// local password. The full signed OIDC exchange must still precede it.
	if _, e = pool.Exec(ctx, `DELETE FROM mfa_factors WHERE user_id=$1`, uid); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE users SET force_mfa=true,password_hash='' WHERE id=$1`, uid); e != nil {
		t.Fatal(e)
	}
	callback, binding = start()
	enrolling := finish(callback, binding)
	if enrolling == nil {
		t.Fatal("external enrollment cookie missing")
	}
	assertCookieDuration(t, enrolling, 300)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, enrolling, secureOrigin), 401)
	info := object(t, call(t, srv.URL, "GET", "/api/account/mfa", nil, enrolling, secureOrigin))
	if info["passwordRequired"] != false || info["phase"] != "mfa_enroll" {
		t.Fatal(info)
	}
	enrollment := object(t, call(t, srv.URL, "POST", "/api/account/mfa/enroll", map[string]string{}, enrolling, secureOrigin))
	newSecret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment["secret"].(string))
	if err != nil {
		t.Fatal(err)
	}
	confirmed := call(t, srv.URL, "POST", "/api/account/mfa/confirm", map[string]string{"totp": identity.TOTP(newSecret, time.Now().Unix()/30)}, enrolling, secureOrigin)
	expect(t, confirmed, 200)
	if confirmed.cookie == nil || !confirmed.cookie.Secure {
		t.Fatal("external enrollment lost Secure")
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, confirmed.cookie, secureOrigin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/account/mfa/enroll", map[string]string{}, enrolling, secureOrigin), 401)
	callback, binding = start()
	if e = a.identity.QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	if finish(callback, binding) != nil {
		t.Fatal("restored login challenge")
	}
}

func TestR13OIDCAnonymousChallengeConsumption(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	id := postID(t, srv, "/api/admin/oidc-providers", map[string]any{"name": "challenge fixture", "issuer": "https://fixture.example", "clientId": "fixture", "clientSecret": "fixture-secret", "redirectUri": "https://backend.localhost:5174/api/oidc/callback", "enabled": true, "password": testPassword}, admin)
	p, e := a.identity.OIDCProvider(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	browser := identity.NewToken()
	issue := func() identity.OIDCAuthorization {
		c := identity.OIDCAuthorization{State: identity.NewToken(), Nonce: identity.NewToken(), Verifier: oauth2.GenerateVerifier(), ExpiresAt: time.Now().Add(time.Minute)}
		if e := a.identity.SaveOIDCLogin(ctx, browser, p, c); e != nil {
			t.Fatal(e)
		}
		return c
	}
	c := issue()
	if _, e = a.identity.ConsumeOIDCLogin(ctx, c.State, identity.NewToken()); e == nil {
		t.Fatal("cross-browser consumption")
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := a.identity.ConsumeOIDCLogin(ctx, c.State, browser); e == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("non-atomic login consumption", wins.Load())
	}
	c = issue()
	if _, e = pool.Exec(ctx, `UPDATE oidc_login_authorizations SET expires_at=now()-interval '1 second'`); e != nil {
		t.Fatal(e)
	}
	if _, e = a.identity.ConsumeOIDCLogin(ctx, c.State, browser); e == nil {
		t.Fatal("expired login accepted")
	}
	c = issue()
	if _, e = pool.Exec(ctx, `UPDATE instance_state SET auth_epoch=auth_epoch+1 WHERE singleton=1`); e != nil {
		t.Fatal(e)
	}
	if _, e = a.identity.ConsumeOIDCLogin(ctx, c.State, browser); e == nil {
		t.Fatal("old epoch accepted")
	}
	c = issue()
	_ = issue()
	if _, e = a.identity.ConsumeOIDCLogin(ctx, c.State, browser); e == nil {
		t.Fatal("replaced browser challenge accepted")
	}
}
