package application

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"puppeteer/backend/internal/modules/identity"
	"puppeteer/backend/internal/modules/notification"
	"puppeteer/backend/internal/modules/system"
	"puppeteer/backend/internal/platform/configstate"
)

func TestExternalDependencyChecksWithoutHostExecutor(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	const secure = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, secure)
	head := func() system.Revision {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	save := func(set map[string]any) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(head().ID), map[string]any{"set": set, "password": testPassword}, admin)
		expect(t, r, 202)
	}
	probe := func(feature, want string) configstate.Task {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/"+feature+"/preflight", configTag(head().ID), map[string]any{}, admin)
		expect(t, r, 202)
		task := configurationTaskResult(t, srv, admin, object(t, r)["id"].(string))
		if task.State != want || task.Generation != nil || task.Evidence == "" {
			t.Fatal(task)
		}
		return task
	}
	for _, id := range []string{"mail", "ldap", "oidcProvider", "wecomProvider", "passkey"} {
		probe(id, "failed")
	}
	certificate, ca := ldapLifecycleCertificate(t)
	messages := make(chan string, 8)
	host, port := contactSMTPFixture(t, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}, messages)
	save(map[string]any{"mail-enabled": false, "mail-use-ssl": false, "mail-host": host, "mail-port": port, "mail-from": "sender@example.test", "mail-ca-pem": ca})
	mailTask := probe("mail", "succeeded")
	select {
	case <-messages:
		t.Fatal("preflight delivered mail")
	default:
	}
	if head().Values.Bool("mail-enabled") {
		t.Fatal("preflight enabled mail")
	}
	address, ldapCA := ldapLifecycleServer(t, map[string][]string{"cn": {"Alice"}, "mail": {"alice@example.test"}})
	save(map[string]any{"ldap-url": address, "ldap-user": "cn=service,dc=example,dc=com", "ldap-password": "service-password", "ldap-base-dn": "dc=example,dc=com", "ldap-ca-pem": ldapCA, "ldap-user-search-size-limit": 5})
	var before, after int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	probe("ldap", "succeeded")
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&after); e != nil || after != before {
		t.Fatal("directory check created users", e)
	}
	save(map[string]any{"ldap-password": "invalid-secret-value"})
	probe("ldap", "failed")
	var issuer string
	var calls atomic.Int32
	vendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(identity.OIDCDiscovery{Issuer: issuer, AuthorizationEndpoint: issuer + "/authorize", TokenEndpoint: issuer + "/token", JWKSURI: issuer + "/keys", ResponseTypes: []string{"code"}, SigningAlgorithms: []string{"RS256"}, PKCEMethods: []string{"S256"}})
		case "/wecom/cgi-bin/gettoken":
			if r.URL.Query().Get("corpsecret") != "vendor-secret-value" {
				t.Error("credential mismatch")
			}
			json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "never-return-token", "expires_in": 7200})
		default:
			t.Errorf("unexpected business operation %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer vendor.Close()
	issuer = vendor.URL
	a.oidcHTTPClient = vendor.Client()
	save(map[string]any{"oidc-issuer": issuer, "oidc-client-id": "fixture", "oidc-client-secret": "vendor-secret-value", "oidc-redirect-uri": secure + "/api/oidc/callback"})
	probe("oidcProvider", "succeeded")
	save(map[string]any{"wechat-work-api-url": issuer + "/wecom", "wechat-work-authorization-url": issuer + "/login", "wechat-work-corp-id": "fixture", "wechat-work-agent-id": "100001", "wechat-work-secret": "vendor-secret-value", "wechat-work-redirect-uri": secure + "/api/wechat-work/callback"})
	probe("wecomProvider", "succeeded")
	if calls.Load() != 2 {
		t.Fatal("unexpected requests", calls.Load())
	}
	a.config.PasskeyRPID = "backend.localhost"
	a.config.PasskeyOrigins = []string{secure}
	probe("passkey", "succeeded")
	status := func() []configurationFeature {
		t.Helper()
		r := call(t, srv.URL, "GET", "/api/settings/operations", nil, admin, origin)
		expect(t, r, 200)
		var body struct{ Items []configurationFeature }
		if json.Unmarshal(r.body, &body) != nil {
			t.Fatal(string(r.body))
		}
		return body.Items
	}
	for _, item := range status() {
		if item.ID == "mail" && (item.Dependencies.State != "stale" || item.LastCheck.ID != mailTask.ID) {
			t.Fatal(item)
		}
		if item.ID == "passkey" && (item.Dependencies.State != "ready" || item.Service.State != "unknown") {
			t.Fatal(item)
		}
	}
	a.config.PasskeyOrigins = []string{"https://other.example.test"}
	for _, item := range status() {
		if item.ID == "passkey" && item.Dependencies.State != "stale" {
			t.Fatal(item)
		}
	}
	probe("passkey", "failed")
	r := call(t, srv.URL, "GET", "/api/settings/operation-tasks", nil, admin, origin)
	expect(t, r, 200)
	for _, secret := range []string{"vendor-secret-value", "invalid-secret-value", "service-password", "never-return-token"} {
		if strings.Contains(string(r.body), secret) {
			t.Fatal("preflight leaked credential")
		}
	}
}

func TestResourceDependencyRevisionAndPermissions(t *testing.T) {
	_, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	certificate, ca := ldapLifecycleCertificate(t)
	messages := make(chan string, 8)
	host, port := contactSMTPFixture(t, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}, messages)
	channel := notification.Channel{Name: "isolated preflight channel", Type: "smtp", Config: notification.Config{Host: host, Port: port, From: "sender@example.test", CA: ca}}
	id := postID(t, srv, "/api/admin/notifications/channels", channel, admin)
	feature := "notification-channel:" + id
	status := func() map[string]any {
		t.Helper()
		r := call(t, srv.URL, "GET", "/api/settings/operations/"+feature, nil, admin, origin)
		expect(t, r, 200)
		return object(t, r)
	}
	snapshot := status()
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	r, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/"+feature+"/preflight", tag, map[string]any{}, admin)
	expect(t, r, 409)
	r, _ = settingsRequest(t, srv.URL, "POST", "/api/settings/operations/"+feature+"/preflight", tag, map[string]any{"evidence": snapshot["evidence"]}, admin)
	expect(t, r, 202)
	if task := configurationTaskResult(t, srv, admin, object(t, r)["id"].(string)); task.State != "succeeded" {
		t.Fatal(task)
	}
	channel.ID = id
	channel.Version = 1
	channel.Name = "changed"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/notifications/channels/"+id, channel, admin, origin), 200)
	if status()["items"].([]any)[0].(map[string]any)["dependencies"].(map[string]any)["state"] != "stale" {
		t.Fatal("resource edit did not expire evidence")
	}
	r, _ = settingsRequest(t, srv.URL, "POST", "/api/settings/operations/"+feature+"/preflight", tag, map[string]any{"evidence": snapshot["evidence"]}, admin)
	expect(t, r, 202)
	if task := configurationTaskResult(t, srv, admin, object(t, r)["id"].(string)); task.State != "failed" {
		t.Fatal("stale submitted resource accepted", task)
	}
	postID(t, srv, "/api/admin/users", userInput("resource-reader", nil, nil), admin)
	member := loginAs(t, srv, "resource-reader")
	expect(t, call(t, srv.URL, "GET", "/api/settings/operations/"+feature, nil, member, origin), 403)
	r, _ = settingsRequest(t, srv.URL, "POST", "/api/settings/operations/"+feature+"/preflight", tag, map[string]any{"evidence": status()["evidence"]}, member)
	expect(t, r, 403)
	select {
	case <-messages:
		t.Fatal("resource check delivered mail")
	default:
	}
	var revision int64
	if e := a.db.Row(ctx, `SELECT version FROM notification_channels WHERE id=$1`, id).Scan(&revision); e != nil || revision != 2 {
		t.Fatal("preflight modified channel", e, revision)
	}
}
