package application

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
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

func TestR13OIDCAuthorizationCallback(t *testing.T) {
	_, a, srv := r02Server(t, false)
	const origin = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, origin)
	admin := loginAs(t, srv, "accept-admin")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer, nonce, pkce string
	bad := false
	idp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(identity.OIDCDiscovery{Issuer: issuer, AuthorizationEndpoint: issuer + "/authorize", TokenEndpoint: issuer + "/token", JWKSURI: issuer + "/keys", ResponseTypes: []string{"code"}, SigningAlgorithms: []string{"RS256"}, PKCEMethods: []string{"S256"}})
		case "/token":
			r.ParseForm()
			user, password, ok := r.BasicAuth()
			if !ok || user != "test-client" || password != "fixture-secret" || oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != pkce {
				t.Error("code exchange proof invalid")
			}
			if bad {
				nonce = "incorrect"
			}
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": issuer, "sub": "fixture-user", "aud": "test-client", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "nonce": nonce})
			token.Header["kid"] = "fixture"
			raw, e := token.SignedString(key)
			if e != nil {
				t.Error(e)
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture", "token_type": "Bearer", "id_token": raw})
		case "/keys":
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "fixture", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer idp.Close()
	issuer = idp.URL
	a.oidcHTTPClient = idp.Client()
	in := map[string]any{"name": "authorization fixture", "issuer": issuer, "clientId": "test-client", "clientSecret": "fixture-secret", "redirectUri": origin + "/api/oidc/callback", "password": testPassword}
	id := postID(t, srv, "/api/admin/oidc-providers", in, admin)
	path := "/api/admin/oidc-providers/" + id + "/authorize-test"
	expect(t, call(t, srv.URL, "POST", path, map[string]any{"password": "wrong"}, admin, origin), 400)
	for _, scenario := range []string{"success", "bad-nonce", "config-changed", "other-browser"} {
		t.Run(scenario, func(t *testing.T) {
			a.mu.Lock()
			a.attempts = map[string]attempt{}
			a.mu.Unlock()
			response := object(t, call(t, srv.URL, "POST", path, map[string]any{"password": testPassword}, admin, origin))
			u, e := url.Parse(response["authorizationUrl"].(string))
			if e != nil {
				t.Fatal(e)
			}
			nonce = u.Query().Get("nonce")
			pkce = u.Query().Get("code_challenge")
			bad = scenario == "bad-nonce"
			callback := "/api/oidc/callback?" + url.Values{"state": {u.Query().Get("state")}, "code": {"fixture-code"}}.Encode()
			expected := 200
			if bad {
				expected = 422
			}
			if scenario == "config-changed" {
				in["version"] = 1
				expect(t, call(t, srv.URL, "PUT", "/api/admin/oidc-providers/"+id, in, admin, origin), 200)
				expected = 400
			}
			if scenario == "other-browser" {
				other := loginAs(t, srv, "accept-admin")
				expect(t, call(t, srv.URL, "GET", callback, nil, other, origin), 400)
			}
			expect(t, call(t, srv.URL, "GET", callback, nil, admin, origin), expected)
			expect(t, call(t, srv.URL, "GET", callback, nil, admin, origin), 400)
		})
	}
	t.Run("personal-binding", func(t *testing.T) {
		a.mu.Lock()
		a.attempts = map[string]attempt{}
		a.mu.Unlock()
		bad = false
		postID(t, srv, "/api/admin/users", userInput("oidc-owner", nil, nil), admin)
		postID(t, srv, "/api/admin/users", userInput("oidc-other", nil, nil), admin)
		owner := loginAs(t, srv, "oidc-owner")
		other := loginAs(t, srv, "oidc-other")
		bindPath := "/api/account/oidc-providers/" + id + "/bind"
		proof := map[string]any{"password": testPassword}
		expect(t, call(t, srv.URL, "POST", bindPath, proof, owner, origin), 403)
		in["version"] = 2
		in["bindingEnabled"] = true
		expect(t, call(t, srv.URL, "PUT", "/api/admin/oidc-providers/"+id, in, admin, origin), 200)
		start := func(cookie *http.Cookie) string {
			a.mu.Lock()
			a.attempts = map[string]attempt{}
			a.mu.Unlock()
			r := call(t, srv.URL, "POST", bindPath, proof, cookie, origin)
			expect(t, r, 200)
			u, e := url.Parse(object(t, r)["authorizationUrl"].(string))
			if e != nil {
				t.Fatal(e)
			}
			nonce = u.Query().Get("nonce")
			pkce = u.Query().Get("code_challenge")
			return "/api/oidc/callback?" + url.Values{"state": {u.Query().Get("state")}, "code": {"fixture-code"}}.Encode()
		}
		callback := start(owner)
		expect(t, call(t, srv.URL, "GET", callback, nil, other, origin), 400)
		expect(t, call(t, srv.URL, "GET", callback, nil, owner, origin), 200)
		expect(t, call(t, srv.URL, "GET", callback, nil, owner, origin), 400)
		own := object(t, call(t, srv.URL, "GET", "/api/account/oidc-identities", nil, owner, origin))
		items := own["items"].([]any)
		if len(items) != 1 {
			t.Fatal("binding missing")
		}
		record := items[0].(map[string]any)
		if record["subject"] != "fixture-user" {
			t.Fatal("unverified subject")
		}
		theirs := object(t, call(t, srv.URL, "GET", "/api/account/oidc-identities", nil, other, origin))
		if len(theirs["items"].([]any)) != 0 {
			t.Fatal("cross-user listing")
		}
		callback = start(other)
		expect(t, call(t, srv.URL, "GET", callback, nil, other, origin), 409)
		unlink := "/api/account/oidc-identities/" + record["id"].(string)
		expect(t, call(t, srv.URL, "DELETE", unlink, proof, other, origin), 403)
		callback = start(owner)
		consumedURL, _ := url.Parse(callback)
		staleProvider, staleChallenge, e := a.identity.ConsumeOIDCAuthorization(context.Background(), consumedURL.Query().Get("state"), owner.Value, object(t, call(t, srv.URL, "GET", "/api/account/info", nil, owner, origin))["id"].(string), "bind")
		if e != nil {
			t.Fatal(e)
		}
		expect(t, call(t, srv.URL, "DELETE", unlink, map[string]any{"password": "wrong"}, owner, origin), 400)
		expect(t, call(t, srv.URL, "DELETE", unlink, proof, owner, origin), 200)
		ownerID := object(t, call(t, srv.URL, "GET", "/api/account/info", nil, owner, origin))["id"].(string)
		staleProvider.Issuer = issuer
		e = a.db.Within(context.Background(), func(ctx context.Context) error {
			if err := a.identity.LockUser(ctx, ownerID); err != nil {
				return err
			}
			return a.identity.BindExternalIdentity(ctx, ownerID, staleProvider, identity.OIDCSubject{Issuer: issuer, Subject: "fixture-user"}, staleChallenge.UserVersion)
		})
		if e == nil {
			t.Fatal("late consumed callback restored unbound identity")
		}

		expect(t, call(t, srv.URL, "GET", callback, nil, owner, origin), 400)
		callback = start(other)
		expect(t, call(t, srv.URL, "GET", callback, nil, other, origin), 200)
		callback = start(owner)
		in["version"] = 3
		in["bindingEnabled"] = false
		expect(t, call(t, srv.URL, "PUT", "/api/admin/oidc-providers/"+id, in, admin, origin), 200)
		expect(t, call(t, srv.URL, "GET", callback, nil, owner, origin), 400)
	})

}

func TestR13OIDCAuthorizationStorage(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	const origin = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, origin)
	admin := loginAs(t, srv, "accept-admin")
	uid := object(t, call(t, srv.URL, "GET", "/api/account/info", nil, admin, origin))["id"].(string)
	id := postID(t, srv, "/api/admin/oidc-providers", map[string]any{"name": "storage", "issuer": "https://issuer.example", "clientId": "fixture", "clientSecret": "secret", "redirectUri": origin + "/api/oidc/callback", "password": testPassword}, admin)
	ctx := context.Background()
	p, e := a.identity.OIDCProvider(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	issue := func() identity.OIDCAuthorization {
		c := identity.OIDCAuthorization{State: identity.NewToken(), Nonce: identity.NewToken(), Verifier: oauth2.GenerateVerifier(), ExpiresAt: time.Now().Add(time.Minute)}
		if e := a.identity.SaveOIDCAuthorization(ctx, uid, "browser", "test", p, c); e != nil {
			t.Fatal(e)
		}
		return c
	}
	c := issue()
	for _, v := range []struct{ browser, purpose string }{{"wrong", "test"}, {"browser", "bind"}} {
		if _, _, e := a.identity.ConsumeOIDCAuthorization(ctx, c.State, v.browser, uid, v.purpose); e == nil {
			t.Fatal("binding bypass")
		}
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, e := a.identity.ConsumeOIDCAuthorization(ctx, c.State, "browser", uid, "test"); e == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("consumption not atomic", wins.Load())
	}
	c = issue()
	if _, e := pool.Exec(ctx, `UPDATE oidc_authorizations SET expires_at=now()-interval '1 second'`); e != nil {
		t.Fatal(e)
	}
	if _, _, e := a.identity.ConsumeOIDCAuthorization(ctx, c.State, "browser", uid, "test"); e == nil {
		t.Fatal("expired challenge accepted")
	}
	c = issue()
	if e := a.identity.RevokeAll(ctx, uid); e != nil {
		t.Fatal(e)
	}
	if _, _, e := a.identity.ConsumeOIDCAuthorization(ctx, c.State, "browser", uid, "test"); e == nil {
		t.Fatal("revoked challenge accepted")
	}
	c = issue()
	if e := a.identity.QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	if _, _, e := a.identity.ConsumeOIDCAuthorization(ctx, c.State, "browser", uid, "test"); e == nil {
		t.Fatal("restored challenge accepted")
	}
}
