package application

import (
	"context"
	"encoding/json"
	"errors"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"testing"
)

// Native protocol fixtures still use the real configuration owner and key store.
// Tests that exercise publication barriers register their nodes explicitly.
func nativeFixtureSetting(ctx context.Context, a *App, enabled bool) error {
	change := func(c context.Context) error {
		var lockedRevision int64
		if e := a.db.Row(c, `SELECT revision FROM configuration_head WHERE singleton=1 FOR UPDATE`).Scan(&lockedRevision); e != nil {
			return e
		}
		v, e := a.system.Configuration(c, 0)
		if e != nil {
			return e
		}
		issuer := v.Values.String(system.OAuthServerIssuerKey)
		if issuer == "" {
			issuer = "https://native.test"
		}
		raw, _ := json.Marshal(issuer)
		on, _ := json.Marshal(enabled)
		if e = a.identity.EnsureOAuthKey(c, false); e != nil {
			return e
		}
		_, e = a.system.SaveConfiguration(system.WithOAuthServerConfigurationProof(c), nil, map[string]json.RawMessage{system.OAuthServerIssuerKey: raw, system.NativeClientEnabledKey: on}, "")
		return e
	}
	if a.db.InTransaction(ctx) {
		return change(ctx)
	}
	return a.db.Within(ctx, change)
}

func TestR16OAuthServerRevisionBarrierAndLegacyEditor(t *testing.T) {
	_, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	const issuer = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, issuer, "https://other.example")
	head := func() system.Revision {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	actual := func() identity.OAuthSettings {
		t.Helper()
		v, e := a.identity.OAuthSettings(ctx)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	apply := func(node string) {
		t.Helper()
		if _, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	save := func(set map[string]any, password string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(head().ID), map[string]any{"set": set, "password": password}, admin)
		expect(t, r, want)
	}
	original := head().ID
	save(map[string]any{system.OAuthServerEnabledKey: true}, "", 422)
	save(map[string]any{system.OAuthServerEnabledKey: true}, testPassword, 422)
	save(map[string]any{system.OAuthServerIssuerKey: "https://untrusted.example"}, testPassword, 422)
	save(map[string]any{system.OAuthServerIssuerKey: issuer + "/"}, testPassword, 422)
	if head().ID != original {
		t.Fatal("invalid configuration committed")
	}
	postID(t, srv, "/api/admin/users", userInput("oauth-config-member", nil, nil), admin)
	member := loginAs(t, srv, "oauth-config-member")
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(head().ID), map[string]any{"set": map[string]any{system.OAuthServerIssuerKey: issuer}, "password": testPassword}, member)
	expect(t, r, 403)
	expect(t, call(t, srv.URL, "GET", "/api/admin/oauth-server", nil, member, origin), 403)
	e := a.db.Within(ctx, func(c context.Context) error {
		_, e := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{system.OAuthServerIssuerKey: json.RawMessage(`"` + issuer + `"`)}, "")
		return e
	})
	if !errors.Is(e, fault.Denied) {
		t.Fatal("service proof bypass", e)
	}
	apply("oauth-a")
	apply("oauth-b")
	pendingVersion := actual().Version
	save(map[string]any{system.OAuthServerIssuerKey: issuer, system.OAuthServerEnabledKey: true}, testPassword, 202)
	if actual().Enabled {
		t.Fatal("enabled before ACK")
	}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/oauth-server", map[string]any{"issuer": issuer, "enabled": false, "version": pendingVersion, "password": testPassword}, admin, origin), 409)
	expect(t, call(t, srv.URL, "GET", "/.well-known/openid-configuration", nil, nil, ""), 503)
	projection := object(t, call(t, srv.URL, "GET", "/api/admin/oauth-server", nil, admin, origin))
	if projection["desiredEnabled"] != true || projection["pending"] != true {
		t.Fatal("pending projection")
	}
	apply("oauth-a")
	if actual().Enabled {
		t.Fatal("one ACK released policy")
	}
	apply("oauth-b")
	expect(t, call(t, srv.URL, "GET", "/.well-known/openid-configuration", nil, nil, ""), 200)
	before := actual()
	expect(t, call(t, srv.URL, "PUT", "/api/admin/oauth-server", map[string]any{"issuer": issuer, "enabled": false, "nativeEnabled": true, "version": before.Version, "password": testPassword}, admin, origin), 200)
	if actual().Enabled || actual().NativeEnabled {
		t.Fatal("mixed tightening/relaxing bypass")
	}
	if head().Values.Bool(system.OAuthServerEnabledKey) || !head().Values.Bool(system.NativeClientEnabledKey) {
		t.Fatal("legacy editor not unified")
	}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/oauth-server", map[string]any{"issuer": issuer, "enabled": false, "nativeEnabled": false, "version": before.Version, "password": testPassword}, admin, origin), 409)
	apply("oauth-a")
	apply("oauth-b")
	if !actual().NativeEnabled {
		t.Fatal("native ACK")
	}
	save(map[string]any{system.OAuthServerIssuerKey: "https://other.example"}, testPassword, 409)
	save(map[string]any{system.NativeClientEnabledKey: false}, testPassword, 202)
	if actual().NativeEnabled {
		t.Fatal("disable not immediate")
	}
	if e = a.db.Within(ctx, a.identity.QuarantineRestore); e != nil {
		t.Fatal(e)
	}
	if head().Values.Bool(system.OAuthServerEnabledKey) || head().Values.Bool(system.NativeClientEnabledKey) || actual().Enabled || actual().NativeEnabled {
		t.Fatal("restore reopens access")
	}
}

func TestR16OAuthServerFreshMFA(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	secret, _, admin := enroll(t, srv, admin)
	v, e := a.system.Configuration(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	save := func(code string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{system.OAuthServerEnabledKey: false}, "password": testPassword, "totp": code}, admin)
		expect(t, r, want)
	}
	save("", 400)
	save(consumedAdminTOTP(t, a, secret), 400)
	save(freshAdminTOTP(t, a, secret), 202)
}
