package application

import (
	"context"
	"encoding/json"
	"errors"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"testing"
)

func TestR16PasskeyConfigurationBarrierProofAndRestore(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, "https://example.com")
	current := func() system.Revision {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	save := func(set map[string]any, password string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(current().ID), map[string]any{"set": set, "password": password}, admin)
		expect(t, r, want)
	}
	apply := func(node string) {
		t.Helper()
		if _, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	if a.passkeyEnabled(ctx) {
		t.Fatal("unconfigured deployment opened passkey")
	}
	save(map[string]any{"passkey-enabled": false}, "", 422)
	save(map[string]any{"passkey-enabled": false}, testPassword, 202)
	save(map[string]any{"passkey-enabled": true}, testPassword, 503)
	for _, set := range []map[string]any{
		{"passkey-domain": "com", "passkey-origins": "[\"https://example.com\"]"},
		{"passkey-domain": "example.com", "passkey-origins": "[\"http://example.com\"]"},
		{"passkey-domain": "example.com", "passkey-origins": "[\"https://evil.example.net\"]"},
		{"passkey-origins": "null"}, {"passkey-origins": "[] {}"},
	} {
		save(set, testPassword, 422)
	}
	apply("passkey-a")
	apply("passkey-b")
	save(map[string]any{"passkey-enabled": true, "passkey-domain": "example.com", "passkey-origins": "[\"https://example.com\"]"}, testPassword, 202)
	if a.passkeyEnabled(ctx) {
		t.Fatal("opened before ACK")
	}
	apply("passkey-a")
	if a.passkeyEnabled(ctx) {
		t.Fatal("opened after only one ACK")
	}
	apply("passkey-b")
	if !a.passkeyEnabled(ctx) {
		t.Fatal("failed to open after all ACK")
	}
	begin := object(t, call(t, srv.URL, "POST", "/api/passkey/login/start", map[string]string{}, nil, "https://example.com"))
	if begin["token"] == "" {
		t.Fatal("no challenge")
	}
	var count int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM passkey_ceremonies`).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	// No-op settings leave the ceremony usable; a real change invalidates it.
	save(map[string]any{"passkey-enabled": true}, testPassword, 202)
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM passkey_ceremonies`).Scan(&count); e != nil || count != 1 {
		t.Fatal("no-op deleted ceremony", count, e)
	}
	save(map[string]any{"passkey-enabled": false}, testPassword, 202)
	if a.passkeyEnabled(ctx) {
		t.Fatal("disable did not apply immediately")
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/passkeys", nil, admin, origin), 200)
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM passkey_ceremonies`).Scan(&count); e != nil || count != 0 {
		t.Fatal("change retained challenge", count, e)
	}
	e := a.db.Within(ctx, func(c context.Context) error {
		_, e := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{"passkey-enabled": json.RawMessage("true")}, "")
		return e
	})
	if !errors.Is(e, fault.Denied) {
		t.Fatal("generic service bypass", e)
	}
	postID(t, srv, "/api/admin/users", userInput("passkey-settings-member", nil, nil), admin)
	member := loginAs(t, srv, "passkey-settings-member")
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(current().ID), map[string]any{"set": map[string]any{"passkey-enabled": true}, "password": testPassword}, member)
	expect(t, r, 403)
	save(map[string]any{"passkey-enabled": true}, testPassword, 202)
	apply("passkey-a")
	apply("passkey-b")
	if e = a.identity.QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	if current().Values.Bool("passkey-enabled") || a.passkeyEnabled(ctx) {
		t.Fatal("isolated restore retained passkey")
	}
}

func TestR16PasskeySettingsRequireFreshMFA(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	secret, _, admin := enroll(t, srv, admin)
	v, e := a.system.Configuration(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	save := func(code string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{"passkey-enabled": false}, "password": testPassword, "totp": code}, admin)
		expect(t, r, want)
	}
	save("", 400)
	save(consumedAdminTOTP(t, a, secret), 400)
	save(freshAdminTOTP(t, a, secret), 202)
}
