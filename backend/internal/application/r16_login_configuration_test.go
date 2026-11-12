package application

import (
	"context"
	"encoding/json"
	"errors"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"testing"
)

func TestR16PasswordLoginConfigurationProofBarrierRecovery(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	const secure = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, secure)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("login-policy-member", nil, nil), admin)
	member := loginAs(t, srv, "login-policy-member")
	current := func() system.Revision {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	original := current()
	patch := func(value any, password string, tag string) result {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"disable-password-login": value}, "password": password}, admin)
		return r
	}
	expect(t, patch(true, "", configTag(original.ID)), 422)
	expect(t, patch(true, testPassword, configTag(original.ID)), 409)
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(original.ID), map[string]any{"set": map[string]any{"disable-password-login": true}, "password": testPassword}, member)
	expect(t, r, 403)
	expect(t, patch(nil, testPassword, configTag(original.ID)), 422)
	if current().ID != original.ID {
		t.Fatal("rejected policy changed head")
	}
	// The storage service itself rejects generic writes without domain approval.
	e := a.db.Within(ctx, func(ctx context.Context) error {
		_, e := a.system.SaveConfiguration(ctx, nil, map[string]json.RawMessage{"disable-password-login": json.RawMessage("true")}, "")
		return e
	})
	if !errors.Is(e, fault.Denied) {
		t.Fatal(e)
	}
	address, ca := ldapLifecycleServer(t)
	provider := map[string]any{"name": "settings login alternative", "url": address, "caPEM": ca, "baseDN": "dc=example,dc=com", "bindDN": "cn=service,dc=example,dc=com", "bindPassword": "service-password", "searchFilter": "(objectClass=person)", "loginAttribute": "uid", "identityAttribute": "entryUUID", "enabled": true, "bindingEnabled": true, "password": testPassword}
	pid := postID(t, srv, "/api/admin/ldap-providers", provider, admin)
	expect(t, call(t, srv.URL, "POST", "/api/account/ldap-providers/"+pid+"/bind", map[string]string{"username": "alice", "directoryPassword": "user-password", "password": testPassword}, admin, secure), 200)
	// A binding alone is insufficient; a successful fresh alternate login is required.
	expect(t, patch(true, testPassword, configTag(original.ID)), 409)
	expect(t, call(t, srv.URL, "POST", "/api/ldap/login", map[string]string{"source": "ldap", "providerId": pid, "username": "alice", "password": "user-password"}, nil, secure), 200)
	apply := func(node string) {
		t.Helper()
		_, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil })
		if e != nil {
			t.Fatal(e)
		}
	}
	apply("login-node-a")
	apply("login-node-b")
	expect(t, patch(true, testPassword, configTag(original.ID)), 202)
	disabled := current()
	policy, e := a.identity.LoginPolicy(ctx)
	if e != nil || policy.PasswordEnabled || policy.Version != 2 || !disabled.Values.Bool("disable-password-login") {
		t.Fatal(policy, e)
	}
	if enabled, e := a.identity.PasswordLoginEnabled(ctx); e != nil || enabled {
		t.Fatal("tightening not immediate", e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "accept-admin", "password": testPassword}, nil, origin), 403)
	expect(t, patch(false, testPassword, configTag(original.ID)), 409)
	if current().ID != disabled.ID {
		t.Fatal("stale revision changed policy")
	}
	expect(t, patch(false, testPassword, configTag(disabled.ID)), 202)
	apply("login-node-a")
	if enabled, e := a.identity.PasswordLoginEnabled(ctx); e != nil || enabled {
		t.Fatal("relaxation bypassed second node", e)
	}
	apply("login-node-b")
	if enabled, e := a.identity.PasswordLoginEnabled(ctx); e != nil || !enabled {
		t.Fatal("ACK did not release login", e)
	}
	// The old domain endpoint shares the same head, mirror and security barrier.
	policy, _ = a.identity.LoginPolicy(ctx)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/login-policy", map[string]any{"passwordEnabled": false, "version": policy.Version, "password": testPassword}, admin, origin), 200)
	if !current().Values.Bool("disable-password-login") {
		t.Fatal("domain write not mirrored")
	}
	if e = a.EnablePasswordLoginFromCLI(ctx, "test-operator", "recover isolated identity outage"); e != nil {
		t.Fatal(e)
	}
	if enabled, e := a.identity.PasswordLoginEnabled(ctx); e != nil || !enabled || current().Values.Bool("disable-password-login") {
		t.Fatal("break-glass did not recover", e)
	}
	// Expired proof cannot close the entry again, and legacy writes enforce proof too.
	if _, e = pool.Exec(ctx, `UPDATE identity_login_proofs SET verified_at=now()-interval '16 minutes'`); e != nil {
		t.Fatal(e)
	}
	expect(t, patch(true, testPassword, configTag(current().ID)), 409)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/properties", map[string]any{"disable-password-login": true}, admin, origin), 422)
}
