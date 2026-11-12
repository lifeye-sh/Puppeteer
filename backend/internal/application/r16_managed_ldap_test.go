package application

import (
	"context"
	"encoding/json"
	"errors"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/system"
	"strings"
	"testing"
)

func TestR16ManagedLDAPSecretLoginBarrierAndRestore(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	const secure = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, secure)
	address, ca := ldapLifecycleServer(t, map[string][]string{"cn": {"Managed Alice"}, "mail": {"alice@example.test"}})
	head := func() system.Revision {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	request := func(patch map[string]any, proof string, clear []string, want int) result {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(head().ID), map[string]any{"set": patch, "password": proof, "clearSecrets": clear}, admin)
		expect(t, r, want)
		return r
	}
	patch := map[string]any{"ldap-enabled": true, "ldap-binding-enabled": true, "ldap-url": address, "ldap-user": "cn=service,dc=example,dc=com", "ldap-password": "service-password", "ldap-base-dn": "dc=example,dc=com", "ldap-ca-pem": ca, "ldap-user-search-size-limit": 5}
	independent := postID(t, srv, "/api/admin/ldap-providers", map[string]any{"name": "independent", "url": address, "caPEM": ca, "baseDN": "dc=example,dc=com", "bindDN": "cn=service,dc=example,dc=com", "bindPassword": "service-password", "searchFilter": "(objectClass=person)", "loginAttribute": "uid", "identityAttribute": "entryUUID", "password": testPassword}, admin)
	if _, e := a.system.ApplyConfiguration(ctx, "ldap-node", func(system.Revision) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if _, e := a.system.ApplyConfiguration(ctx, "ldap-node-b", func(system.Revision) error { return nil }); e != nil {
		t.Fatal(e)
	}
	request(patch, "", nil, 422)
	request(patch, testPassword, nil, 202)
	var pid string
	if e := pool.QueryRow(ctx, `SELECT id FROM ldap_identity_providers WHERE settings_managed`).Scan(&pid); e != nil {
		t.Fatal(e)
	}
	provider := func() identity.LDAPProvider {
		t.Helper()
		p, e := a.identity.LDAPProvider(ctx, pid)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	p := provider()
	if p.Enabled || p.BindingEnabled || !p.Managed || !p.HasSecret {
		t.Fatal("enabled before ACK", p)
	}
	if _, e := a.system.ApplyConfiguration(ctx, "ldap-node", func(system.Revision) error { return nil }); e != nil {
		t.Fatal(e)
	}
	p = provider()
	if p.Enabled {
		t.Fatal("one ACK enabled a two-node revision")
	}
	if _, e := a.system.ApplyConfiguration(ctx, "ldap-node-b", func(system.Revision) error { return errors.New("fixture preparation failed") }); e == nil {
		t.Fatal("preparation failure accepted")
	}
	if provider().Enabled {
		t.Fatal("failed node enabled LDAP")
	}
	if e := a.system.RetryConfiguration(ctx, head().ID); e != nil {
		t.Fatal(e)
	}
	if _, e := a.system.ApplyConfiguration(ctx, "ldap-node-b", func(system.Revision) error { return nil }); e != nil {
		t.Fatal(e)
	}
	p = provider()
	if !p.Enabled || !p.BindingEnabled {
		t.Fatal("ACK did not enable")
	}
	ref := head().Values.String("ldap-password")
	secret, e := a.identity.LDAPPassword(ctx, pid, a.config.VaultKey)
	if e != nil || secret != "service-password" {
		t.Fatal("managed secret decryption", e)
	}
	for _, path := range []string{"/api/settings", "/api/settings/revisions", "/api/admin/properties", "/api/admin/ldap-providers"} {
		r := call(t, srv.URL, "GET", path, nil, admin, origin)
		expect(t, r, 200)
		if strings.Contains(string(r.body), "service-password") || strings.Contains(string(r.body), ref) {
			t.Fatal("secret leak", path)
		}
	}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ldap-providers/"+pid, map[string]any{"LDAPProvider": p, "password": testPassword}, admin, origin), 400)
	// A correctly shaped legacy edit must also be blocked.
	raw, _ := json.Marshal(p)
	var edit map[string]any
	json.Unmarshal(raw, &edit)
	edit["password"] = testPassword
	edit["settingsManaged"] = false
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ldap-providers/"+pid, edit, admin, origin), 409)
	request(map[string]any{"ldap-user-property-mapping": `{"username":"uid","unknown":"cn"}`}, testPassword, nil, 422)
	request(map[string]any{"ldap-user": "cn=other,dc=example,dc=com"}, testPassword, nil, 422)
	request(map[string]any{"ldap-url": "ldaps://other.example.test:636", "ldap-password": "new-isolated-password"}, testPassword, nil, 409)
	request(map[string]any{}, testPassword, []string{"ldap-password"}, 422)
	request(map[string]any{"ldap-user-search-size-limit": 2}, "", nil, 422)
	postID(t, srv, "/api/admin/users", userInput("managed-ldap-user", nil, nil), admin)
	user := loginAs(t, srv, "managed-ldap-user")
	expect(t, call(t, srv.URL, "POST", "/api/account/ldap-providers/"+pid+"/bind", map[string]string{"username": "alice", "directoryPassword": "user-password", "password": testPassword}, user, secure), 200)
	login := func() result {
		return call(t, srv.URL, "POST", "/api/ldap/login", map[string]string{"source": "ldap", "providerId": pid, "username": "alice", "password": "user-password"}, nil, secure)
	}
	expect(t, login(), 200)
	preview := call(t, srv.URL, "POST", "/api/admin/ldap-providers/"+pid+"/sync-preview", map[string]any{"password": testPassword}, admin, origin)
	expect(t, preview, 200)
	if object(t, preview)["entries"].([]any)[0].(map[string]any)["nickname"] != "Managed Alice" {
		t.Fatal("default mapping not used")
	}
	request(map[string]any{"ldap-enabled": false, "ldap-binding-enabled": false}, testPassword, nil, 202)
	if provider().Enabled || provider().BindingEnabled {
		t.Fatal("disable waits for ACK")
	}
	expect(t, login(), 400)
	request(map[string]any{"ldap-enabled": true, "ldap-binding-enabled": true}, testPassword, nil, 202)
	if provider().Enabled {
		t.Fatal("re-enable skipped barrier")
	}
	if _, e := a.system.ApplyConfiguration(ctx, "ldap-node", func(system.Revision) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if _, e := a.system.ApplyConfiguration(ctx, "ldap-node-b", func(system.Revision) error { return nil }); e != nil {
		t.Fatal(e)
	}
	expect(t, login(), 200)
	// Isolated restore must close both config and materialized provider, preserving encrypted history.
	if e := a.identity.QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	restored := head()
	if restored.Values.Bool("ldap-enabled") || restored.Values.Bool("ldap-binding-enabled") || provider().Enabled || provider().BindingEnabled {
		t.Fatal("restore reopened directory")
	}
	if restored.Values.String("ldap-password") != ref {
		t.Fatal("restore discarded encrypted secret history")
	}
	original, e := a.identity.LDAPProvider(ctx, independent)
	if e != nil || original.Managed || original.URL != address {
		t.Fatal("independent directory overwritten", e)
	}
}

func TestR16ManagedLDAPLastAdminProtection(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	const secure = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, secure)
	address, ca := ldapLifecycleServer(t)
	save := func(values map[string]any, want int) {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": values, "password": testPassword}, admin)
		expect(t, r, want)
	}
	save(map[string]any{"ldap-enabled": true, "ldap-binding-enabled": true, "ldap-url": address, "ldap-user": "cn=service,dc=example,dc=com", "ldap-password": "service-password", "ldap-base-dn": "dc=example,dc=com", "ldap-ca-pem": ca}, 202)
	var pid string
	if e := pool.QueryRow(ctx, `SELECT id FROM ldap_identity_providers WHERE settings_managed`).Scan(&pid); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/account/ldap-providers/"+pid+"/bind", map[string]string{"username": "alice", "directoryPassword": "user-password", "password": testPassword}, admin, secure), 200)
	expect(t, call(t, srv.URL, "POST", "/api/ldap/login", map[string]string{"source": "ldap", "providerId": pid, "username": "alice", "password": "user-password"}, nil, secure), 200)
	save(map[string]any{"disable-password-login": true}, 202)
	before, e := a.system.Configuration(ctx, 0)
	if e != nil {
		t.Fatal(e)
	}
	save(map[string]any{"ldap-enabled": false}, 409)
	save(map[string]any{"ldap-user-search-size-limit": 20}, 409) // Invalidates the only verified provider version.
	after, e := a.system.Configuration(ctx, 0)
	if e != nil || after.ID != before.ID {
		t.Fatal("failed anti-lockout check committed", e)
	}
	p, e := a.identity.LDAPProvider(ctx, pid)
	if e != nil || !p.Enabled {
		t.Fatal("last admin path disabled", e)
	}
	save(map[string]any{"disable-password-login": false}, 202)
	save(map[string]any{"ldap-enabled": false}, 202)
}
