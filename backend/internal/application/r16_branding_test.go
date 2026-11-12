package application

import (
	"context"
	"testing"
)

func TestR16BrandingRevisionAndPublicProjection(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	for _, name := range []any{"", "   ", "name\nheader", 12, nil} {
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"system-name": name, "system-copyright": "must rollback"}}, admin)
		expect(t, r, 422)
	}
	before, err := a.system.Configuration(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	r, newTag := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"system-name": "内网运维", "system-copyright": "Internal operations", "system-icp": "备案测试"}}, admin)
	expect(t, r, 202)
	if newTag == tag {
		t.Fatal("missing branding revision")
	}
	public := object(t, call(t, srv.URL, "GET", "/api/branding", nil, nil, origin))
	if public["name"] != "内网运维" || public["copyright"] != "Internal operations" || public["icp"] != "备案测试" {
		t.Fatal(public)
	}
	if len(public) != 6 {
		t.Fatal("public projection unexpectedly expanded", public)
	}
	history, err := a.system.Configuration(context.Background(), before.ID)
	if err != nil || history.Values.String("system-name") == "内网运维" {
		t.Fatal("history mutated", err)
	}
	expect(t, call(t, srv.URL, "PATCH", "/api/settings", map[string]any{"set": map[string]string{"system-name": "anonymous"}}, nil, origin), 401)
}
