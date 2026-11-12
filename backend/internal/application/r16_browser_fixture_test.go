package application

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"next-terminal/backend/internal/modules/system"
	listenerpkg "next-terminal/backend/internal/platform/listener"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in browser fixture. It can never use the business database: the DSN must
// identify the disposable PostgreSQL service and test database. testPool adds a
// random schema and drops it on exit. Publish only 127.0.0.1:44087 at Docker.
func TestR16IsolatedBrowserFixture(t *testing.T) {
	if os.Getenv("NT_R16_BROWSER_FIXTURE") != "1" {
		t.Skip("explicit isolated browser fixture only")
	}
	dsn, e := url.Parse(os.Getenv("NT_TEST_DATABASE_URL"))
	if e != nil || dsn.Hostname() != "r16-postgres-fixture" || dsn.Path != "/nt_r16_test" {
		t.Fatal("refuse non-disposable database")
	}
	pool, a, fixtureAPI := r02Server(t, false) // Synthetic test accounts only; production captcha is unchanged.
	forwardHost, forwardPort, _ := sshTarget(t)
	forwardAsset := r04Asset(t, fixtureAPI, loginAs(t, fixtureAPI, "accept-admin"), forwardHost, forwardPort, true)
	postID(t, fixtureAPI, "/api/admin/departments", map[string]string{"name": "R16 默认部门验收"}, loginAs(t, fixtureAPI, "accept-admin"))
	const webOrigin = "http://r16-ui.localhost:5185"
	a.config.AllowedOrigins = []string{origin, webOrigin, "https://r16-ui.localhost:5185"}
	a.config.PasskeyRPID = "r16-ui.localhost"
	a.config.PasskeyOrigins = []string{"https://r16-ui.localhost:5185"}
	// Two separate test issuers preserve the platform's global issuer uniqueness.
	wecom := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/cgi-bin/gettoken" || r.URL.Query().Get("corpid") != "browser-corp" || r.URL.Query().Get("corpsecret") != "browser-secret" {
			w.WriteHeader(400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "browser-fixture", "expires_in": 7200})
	}))
	defer wecom.Close()
	var issuer string
	oidc := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys", "response_types_supported": []string{"code"}, "id_token_signing_alg_values_supported": []string{"RS256"}, "code_challenge_methods_supported": []string{"S256"}})
	}))
	defer oidc.Close()
	issuer = oidc.URL
	a.oidcHTTPClient = wecom.Client()
	a.oidcHTTPClient.Transport.(*http.Transport).TLSClientConfig.RootCAs.AddCert(oidc.Certificate())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	address, ca := ldapLifecycleServer(t, map[string][]string{"cn": {"浏览器验收用户"}, "mail": {"browser@example.test"}})
	var actor string
	if e = pool.QueryRow(ctx, `SELECT id FROM users WHERE username='accept-admin'`).Scan(&actor); e != nil {
		t.Fatal(e)
	}
	if e = a.db.Within(ctx, func(c context.Context) error {
		_, e := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{"system-name": json.RawMessage(`"R16 隔离验收"`), "system-copyright": json.RawMessage(`"临时测试实例 · 30分钟自动关闭"`)}, actor)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	managedListeners := os.Getenv("NT_R16_BROWSER_LISTENERS") == "1"
	if managedListeners {
		if e = a.StartPublicListeners(ctx, ":44087", ""); e != nil {
			t.Fatal(e)
		}
		defer a.ClosePublicListeners()
		a.listenerManager = listenerpkg.New(ctx, 32)
		defer func() { a.listenerManager.Close(); a.listenerManager.Wait() }()
	} else {
		listener, e := net.Listen("tcp", ":44087")
		if e != nil {
			t.Fatal(e)
		}
		server := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
		t.Cleanup(func() { _ = server.Close() })
		go func() {
			if e := server.Serve(listener); e != nil && e != http.ErrServerClosed {
				t.Error(e)
				cancel()
			}
		}()
	}
	folder := os.Getenv("NT_R16_BROWSER_OUTPUT")
	if folder == "" {
		t.Fatal("output directory required")
	}
	if e = os.MkdirAll(folder, 0700); e != nil {
		t.Fatal(e)
	}
	filename := filepath.Join(folder, "fixture.json")
	raw, _ := json.Marshal(map[string]any{"forwardAsset": forwardAsset, "forwardHost": forwardHost, "forwardPort": forwardPort, "oidcIssuer": issuer, "wecomAPI": wecom.URL, "wecomAuthorization": wecom.URL + "/login", "origin": webOrigin, "username": "accept-admin", "password": testPassword, "ldapURL": address, "ldapCA": ca, "bindDN": "cn=service,dc=example,dc=com", "bindPassword": "service-password", "baseDN": "dc=example,dc=com", "expiresAt": time.Now().Add(30 * time.Minute)})
	if e = os.WriteFile(filename, raw, 0600); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.Remove(filename) })
	t.Log("isolated browser fixture ready; synthetic credentials in configured local output only")
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if managedListeners {
				if _, e := a.appliedConfiguration(ctx); e != nil && !errors.Is(e, system.ErrApplyFailed) {
					t.Error(e)
					return
				}
			} else {
				if _, e := a.system.ApplyConfiguration(ctx, "isolated-browser", func(system.Revision) error { return nil }); e != nil {
					t.Error(e)
					return
				}
			}
			if _, e := os.Stat(filepath.Join(folder, "stop")); e == nil {
				return
			}
		}
	}
}
