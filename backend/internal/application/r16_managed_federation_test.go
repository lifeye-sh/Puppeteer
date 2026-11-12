package application

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"testing"
	"time"
)

func TestR16ManagedFederationLoginSecretsAndBarrier(t *testing.T) {
	for _, kind := range []string{"oidc", "wecom"} {
		t.Run(kind, func(t *testing.T) {
			pool, a, srv := r02Server(t, false)
			ctx := context.Background()
			admin := loginAs(t, srv, "accept-admin")
			const secure = "https://backend.localhost:5174"
			a.config.AllowedOrigins = append(a.config.AllowedOrigins, secure)
			postID(t, srv, "/api/admin/users", userInput("federation-owner", nil, nil), admin)
			owner := loginAs(t, srv, "federation-owner")
			key, e := rsa.GenerateKey(rand.Reader, 2048)
			if e != nil {
				t.Fatal(e)
			}
			var issuer, nonce, challenge string
			subject := "member-1"
			vendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/.well-known/openid-configuration":
					json.NewEncoder(w).Encode(identity.OIDCDiscovery{Issuer: issuer, AuthorizationEndpoint: issuer + "/authorize", TokenEndpoint: issuer + "/token", JWKSURI: issuer + "/keys", ResponseTypes: []string{"code"}, SigningAlgorithms: []string{"RS256"}, PKCEMethods: []string{"S256"}})
				case "/token":
					r.ParseForm()
					client, secret, ok := r.BasicAuth()
					if !ok || client != "fixture-client" || secret != "fixture-secret" || oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != challenge {
						t.Error("managed OIDC credentials/PKCE not used")
					}
					token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": issuer, "sub": subject, "aud": "fixture-client", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "nonce": nonce})
					token.Header["kid"] = "fixture"
					raw, e := token.SignedString(key)
					if e != nil {
						t.Error(e)
					}
					json.NewEncoder(w).Encode(map[string]string{"access_token": "fixture", "token_type": "Bearer", "id_token": raw})
				case "/keys":
					json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "fixture", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
				case "/cgi-bin/gettoken":
					if r.URL.Query().Get("corpid") != "fixture-client" || r.URL.Query().Get("corpsecret") != "fixture-secret" {
						t.Error("managed WeCom secret not used")
					}
					json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "fixture", "expires_in": 7200})
				case "/cgi-bin/auth/getuserinfo":
					if r.URL.Query().Get("access_token") != "fixture" || r.URL.Query().Get("code") != "fixture-code" {
						t.Error("WeCom exchange")
					}
					json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "userid": subject})
				default:
					w.WriteHeader(404)
				}
			}))
			defer vendor.Close()
			issuer = vendor.URL
			a.oidcHTTPClient = vendor.Client()
			head := func() system.Revision {
				t.Helper()
				v, e := a.system.Configuration(ctx, 0)
				if e != nil {
					t.Fatal(e)
				}
				return v
			}
			save := func(set map[string]any, password string, clear []string, want int) {
				t.Helper()
				r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(head().ID), map[string]any{"set": set, "password": password, "clearSecrets": clear}, admin)
				expect(t, r, want)
			}
			prefix, secretKey, callbackPath := "oidc", "oidc-client-secret", "/api/oidc/callback"
			patch := map[string]any{"oidc-enabled": true, "oidc-binding-enabled": true, "oidc-issuer": issuer, "oidc-client-id": "fixture-client", "oidc-client-secret": "fixture-secret", "oidc-redirect-uri": secure + callbackPath, "oidc-scopes": "openid profile"}
			if kind == "wecom" {
				prefix = "wechat-work"
				secretKey = "wechat-work-secret"
				callbackPath = "/api/wechat-work/callback"
				patch = map[string]any{"wechat-work-enabled": true, "wechat-work-binding-enabled": true, "wechat-work-api-url": issuer, "wechat-work-authorization-url": issuer + "/login", "wechat-work-corp-id": "fixture-client", "wechat-work-agent-id": "100001", "wechat-work-secret": "fixture-secret", "wechat-work-redirect-uri": secure + callbackPath}
			}
			independent := postID(t, srv, "/api/admin/oidc-providers", map[string]any{"name": "independent", "issuer": issuer + "/independent", "clientId": "independent", "clientSecret": "independent-secret", "redirectUri": secure + "/api/oidc/callback", "password": testPassword}, admin)
			apply := func(node string) {
				t.Helper()
				if _, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
					t.Fatal(e)
				}
			}
			apply("federation-a")
			apply("federation-b")
			save(patch, "", nil, 422)
			save(patch, testPassword, nil, 202)
			var pid string
			if e = pool.QueryRow(ctx, `SELECT id FROM oidc_identity_providers WHERE settings_managed AND kind=$1`, kind).Scan(&pid); e != nil {
				t.Fatal(e)
			}
			provider := func() identity.OIDCProvider {
				t.Helper()
				p, e := a.identity.OIDCProvider(ctx, pid)
				if e != nil {
					t.Fatal(e)
				}
				return p
			}
			if p := provider(); p.Enabled || p.BindingEnabled || !p.Managed || !p.HasSecret {
				t.Fatal("premature activation", p)
			}
			apply("federation-a")
			if provider().Enabled {
				t.Fatal("single ACK opened login")
			}
			apply("federation-b")
			if !provider().Enabled || !provider().BindingEnabled {
				t.Fatal("ACK activation")
			}
			ref := head().Values.String(secretKey)
			for _, path := range []string{"/api/settings", "/api/settings/revisions", "/api/admin/properties", "/api/admin/oidc-providers"} {
				r := call(t, srv.URL, "GET", path, nil, admin, origin)
				expect(t, r, 200)
				if strings.Contains(string(r.body), "fixture-secret") || strings.Contains(string(r.body), ref) {
					t.Fatal("secret/reference disclosed", path)
				}
			}
			expect(t, call(t, srv.URL, "POST", "/api/admin/oidc-providers/"+pid+"/test", nil, admin, origin), 200)
			p := provider()
			legacy := map[string]any{"name": p.Name, "kind": p.Kind, "issuer": p.Issuer, "clientId": p.ClientID, "agentId": p.AgentID, "authorizationUrl": p.AuthorizationURL, "redirectUri": p.RedirectURI, "version": p.Version, "password": testPassword}
			expect(t, call(t, srv.URL, "PUT", "/api/admin/oidc-providers/"+pid, legacy, admin, origin), 403)
			e = a.db.Within(ctx, func(c context.Context) error {
				_, e := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{prefix + "-enabled": json.RawMessage("false")}, "")
				return e
			})
			if !errors.Is(e, fault.Denied) {
				t.Fatal("service bypass", e)
			}
			callback := func(r result) string {
				t.Helper()
				expect(t, r, 200)
				u, e := url.Parse(object(t, r)["authorizationUrl"].(string))
				if e != nil {
					t.Fatal(e)
				}
				nonce = u.Query().Get("nonce")
				challenge = u.Query().Get("code_challenge")
				if kind == "oidc" && u.Query().Get("scope") != "openid profile" {
					t.Fatal("scopes not applied", u.Query().Get("scope"))
				}
				return callbackPath + "?" + url.Values{"state": {u.Query().Get("state")}, "code": {"fixture-code"}}.Encode()
			}
			finish := func(path string, cookie *http.Cookie) (int, *http.Cookie) {
				t.Helper()
				req, _ := http.NewRequest("GET", srv.URL+path, nil)
				if cookie != nil {
					req.AddCookie(cookie)
				}
				client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
				res, e := client.Do(req)
				if e != nil {
					t.Fatal(e)
				}
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
				for _, c := range res.Cookies() {
					if c.Name == CookieName && c.Value != "" {
						return res.StatusCode, c
					}
				}
				return res.StatusCode, nil
			}
			bound := call(t, srv.URL, "POST", "/api/account/oidc-providers/"+pid+"/bind", map[string]string{"password": testPassword}, owner, secure)
			if status, _ := finish(callback(bound), owner); status != 200 {
				t.Fatal("binding", status)
			}
			start := call(t, srv.URL, "POST", "/api/oidc/login/"+pid+"/start", map[string]string{}, nil, secure)
			path := callback(start)
			_, logged := finish(path, start.cookie)
			if logged == nil {
				t.Fatal("managed login failed")
			}
			expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, logged, secure), 200)
			// Real alternate-login proof must protect the final administrator path.
			subject = "admin-1"
			adminBind := call(t, srv.URL, "POST", "/api/account/oidc-providers/"+pid+"/bind", map[string]string{"password": testPassword}, admin, secure)
			if status, _ := finish(callback(adminBind), admin); status != 200 {
				t.Fatal("admin binding", status)
			}
			adminStart := call(t, srv.URL, "POST", "/api/oidc/login/"+pid+"/start", map[string]string{}, nil, secure)
			if _, c := finish(callback(adminStart), adminStart.cookie); c == nil {
				t.Fatal("admin alternate proof")
			}
			save(map[string]any{"disable-password-login": true}, testPassword, nil, 202)
			save(map[string]any{prefix + "-enabled": false}, testPassword, nil, 409)
			if !provider().Enabled {
				t.Fatal("last administrator path disabled")
			}
			save(map[string]any{"disable-password-login": false}, testPassword, nil, 202)
			apply("federation-a")
			apply("federation-b")
			subject = "member-1"
			// Changed provider invalidates both existing sessions and in-flight callbacks.
			started := call(t, srv.URL, "POST", "/api/oidc/login/"+pid+"/start", map[string]string{}, nil, secure)
			path = callback(started)
			save(map[string]any{prefix + "-enabled": false}, testPassword, nil, 202)
			if provider().Enabled {
				t.Fatal("disable delayed")
			}
			if _, c := finish(path, started.cookie); c != nil {
				t.Fatal("old challenge accepted")
			}
			expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, logged, secure), 401)
			if kind == "oidc" {
				save(map[string]any{"oidc-issuer": "https://other.invalid", "oidc-client-secret": "new-secret"}, testPassword, nil, 409)
				save(map[string]any{"oidc-client-id": "other"}, testPassword, nil, 422)
				save(map[string]any{"oidc-scopes": "profile"}, testPassword, nil, 422)
			} else {
				save(map[string]any{"wechat-work-corp-id": "other", "wechat-work-secret": "new-secret"}, testPassword, nil, 409)
			}
			old, e := a.identity.OIDCProvider(ctx, independent)
			if e != nil || old.Version != 1 || old.Managed {
				t.Fatal("independent provider changed", e)
			}
			if e = a.identity.QuarantineRestore(ctx); e != nil {
				t.Fatal(e)
			}
			if head().Values.Bool(prefix+"-enabled") || head().Values.Bool(prefix+"-binding-enabled") || provider().Enabled || provider().BindingEnabled {
				t.Fatal("restore reopened login")
			}
			if head().Values.String(secretKey) != ref {
				t.Fatal("restore destroyed secret history")
			}
		})
	}
}

func TestR16FederationConfigurationFreshMFA(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	secret, _, admin := enroll(t, srv, admin)
	v, e := a.system.Configuration(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	save := func(code string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{"oidc-scopes": "openid profile"}, "password": testPassword, "totp": code}, admin)
		expect(t, r, want)
	}
	save("", 400)
	save(consumedAdminTOTP(t, a, secret), 400)
	save(freshAdminTOTP(t, a, secret), 202)
}
