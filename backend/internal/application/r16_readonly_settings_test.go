package application

import (
	"context"
	"testing"
)

func TestR16ReadonlySettingsProjectionCannotBeMutated(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	read := object(t, call(t, srv.URL, "GET", "/api/admin/properties", nil, admin, origin))
	if read["version"] != Release || read["security-level"] != "custom" || read["ssh-server-private-key-exists"] != false {
		t.Fatal("read-only projection mismatch")
	}
	v, e := a.system.Configuration(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	r, _ := settingsRequest(t, srv.URL, "PUT", "/api/admin/properties", configTag(v.ID), map[string]any{"version": Release, "security-level": "custom", "ssh-server-private-key-exists": false}, admin)
	expect(t, r, 204)
	latest, e := a.system.Configuration(context.Background(), 0)
	if e != nil || latest.ID != v.ID {
		t.Fatal("read-only round trip created a revision", e)
	}
	for _, change := range []map[string]any{{"version": "forged-release"}, {"security-level": "low"}, {"ssh-server-private-key-exists": true}, {"version": nil}, {"ssh-server-private-key-exists": "false"}} {
		r, _ := settingsRequest(t, srv.URL, "PUT", "/api/admin/properties", configTag(v.ID), change, admin)
		expect(t, r, 422)
	}
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{"version": Release}}, admin)
	expect(t, r, 422)
	if _, e = a.nativeGatewayKey(context.Background()); e != nil {
		t.Fatal(e)
	}
	view := object(t, call(t, srv.URL, "GET", "/api/settings", nil, admin, origin))
	metadata := view["readOnly"].(map[string]any)
	if metadata["ssh-server-private-key-exists"] != true || metadata["version"] != Release {
		t.Fatal("key existence not derived from real stored host identity")
	}
	if _, exists := view["values"].(map[string]any)["version"]; exists {
		t.Fatal("operational version inserted into immutable configuration values")
	}
}
