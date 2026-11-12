package application

import (
	"context"
	"encoding/json"
	"errors"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"testing"
)

func TestR16ForwardConfigurationBarrierAndProjection(t *testing.T) {
	_, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	host, port, _ := sshTarget(t)
	asset := r04Asset(t, srv, admin, host, port, true)
	postID(t, srv, "/api/admin/users", userInput("forward-member", nil, nil), admin)
	member := loginAs(t, srv, "forward-member")
	head := func() system.Revision {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	domain := func() nativeForwardSettings {
		t.Helper()
		v, e := a.forwardSettings(ctx)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	save := func(patch map[string]any, password string, status int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(head().ID), map[string]any{"set": patch, "password": password}, admin)
		expect(t, r, status)
	}
	apply := func(node string) {
		t.Helper()
		if _, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	rules := system.EncodeForwardRules([]system.NativeForwardRule{{AssetID: asset, Host: "LOCALHOST", Port: port}})
	patch := map[string]any{system.ForwardEnabledKey: true, system.ForwardRulesKey: rules}
	apply("forward-a")
	apply("forward-b")
	save(patch, "", 422)
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(head().ID), map[string]any{"set": patch, "password": testPassword}, member)
	expect(t, r, 403)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/native-forward", map[string]any{"password": testPassword, "enabled": true, "version": domain().Version, "rules": []map[string]any{{"assetId": asset, "host": "localhost", "port": port}}}, member, origin), 403)
	save(patch, testPassword, 202)
	if domain().Enabled {
		t.Fatal("enabled before ACK")
	}
	apply("forward-a")
	if domain().Enabled {
		t.Fatal("single node enabled forwarding")
	}
	apply("forward-b")
	if !domain().Enabled || len(domain().Rules[0].Addresses) == 0 {
		t.Fatal("ACK/pins", domain())
	}
	if head().Values.String(system.ForwardRulesKey) != system.EncodeForwardRules(domain().Rules) {
		t.Fatal("projection differs")
	}
	// Saving the same hostname explicitly refreshes pins behind a new ACK barrier.
	priorRevision := head().ID
	save(patch, testPassword, 202)
	if domain().Enabled || head().ID == priorRevision {
		t.Fatal("DNS refresh skipped barrier")
	}
	apply("forward-a")
	apply("forward-b")
	// A new target is blocked immediately until both nodes acknowledge it.
	prior := domain().Version
	changed := system.EncodeForwardRules([]system.NativeForwardRule{{AssetID: asset, Host: "127.0.0.1", Port: port}})
	save(map[string]any{system.ForwardRulesKey: changed}, testPassword, 202)
	if domain().Enabled || domain().Version == prior {
		t.Fatal("rule change bypassed barrier")
	}
	apply("forward-a")
	apply("forward-b")
	expect(t, call(t, srv.URL, "PUT", "/api/admin/native-forward", map[string]any{"password": testPassword, "enabled": false, "version": prior, "rules": []any{}}, admin, origin), 409)
	// Submitted DNS pins cannot enter the configuration registry.
	bad := `[{"assetId":"` + asset + `","host":"localhost","port":22,"addresses":["127.0.0.1"]}]`
	save(map[string]any{system.ForwardRulesKey: bad}, testPassword, 422)
	before := head().ID
	bad = system.EncodeForwardRules([]system.NativeForwardRule{{AssetID: asset, Host: "169.254.169.254", Port: 80}})
	save(map[string]any{system.ForwardRulesKey: bad}, testPassword, 422)
	if head().ID != before || !domain().Enabled {
		t.Fatal("failed validation partially committed")
	}
	e := a.db.Within(ctx, func(c context.Context) error {
		_, e := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{system.ForwardEnabledKey: json.RawMessage("false")}, "")
		return e
	})
	if !errors.Is(e, fault.Denied) {
		t.Fatal("proof bypass", e)
	}
	// Legacy editor and unified settings have one authoritative revision.
	expect(t, call(t, srv.URL, "PUT", "/api/admin/native-forward", map[string]any{"password": testPassword, "enabled": false, "version": domain().Version, "rules": []map[string]any{{"assetId": asset, "host": "unresolvable.invalid", "port": 22}}}, admin, origin), 200)
	if domain().Enabled || head().Values.Bool(system.ForwardEnabledKey) || head().ID == before {
		t.Fatal("legacy editor did not publish")
	}
	// Restoring an enabled policy must disable both the desired and actual state.
	save(patch, testPassword, 202)
	apply("forward-a")
	apply("forward-b")
	if e = a.identity.QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	if domain().Enabled || head().Values.Bool(system.ForwardEnabledKey) {
		t.Fatal("restored forwarding enabled")
	}
}

func TestR16ForwardConfigurationFreshMFA(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	secret, _, admin := enroll(t, srv, admin)
	v, e := a.system.Configuration(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	save := func(code string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{system.ForwardEnabledKey: false}, "password": testPassword, "totp": code}, admin)
		expect(t, r, want)
	}
	save("", 400)
	save(consumedAdminTOTP(t, a, secret), 400)
	save(freshAdminTOTP(t, a, secret), 202)
}
