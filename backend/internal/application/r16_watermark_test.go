package application

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"next-terminal/backend/internal/modules/system"
)

func TestR16WatermarkSnapshotAndAuthorizedViews(t *testing.T) {
	host, port, _ := sshTarget(t)
	_, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	reservation := object(t, call(t, srv.URL, "POST", "/api/sessions", map[string]any{"assetId": asset}, admin, origin))
	id := reservation["id"].(string)
	old, e := a.sessions.Get(ctx, id)
	if e != nil || !old.Watermark.Enabled || old.Watermark.Revision == 0 || old.Watermark.Size != 16 || !reflect.DeepEqual(old.Watermark.Content, []string{"受控访问", "用户：accept-admin"}) {
		t.Fatal(old.Watermark, e)
	}
	web, _ := json.Marshal(reservation["watermark"])
	saved, _ := json.Marshal(old.Watermark)
	var webValue system.Watermark
	if json.Unmarshal(web, &webValue) != nil || !reflect.DeepEqual(webValue, old.Watermark) || strings.Contains(string(saved), "remote-only-secret") {
		t.Fatal("reservation snapshot mismatch or secret leak")
	}
	ws := attach(t, srv, admin, id, reservation["ticket"].(string))
	defer ws.Close()
	readUntil(t, ws, "3"+id)
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	for _, patch := range []map[string]any{
		{"watermark-font-color": "url(https://invalid)", "watermark-font-size": 20},
		{"watermark-font-color": "rgba(999,0,0,1)"},
		{"watermark-content": "<script>"},
		{"watermark-content": "{password}"},
		{"watermark-font-size": 101},
		{"watermark-font-size": 0},
		{"watermark-font-size": 2.5},
		{"watermark-enabled": true, "watermark-content": "", "watermark-content-user-account": false, "watermark-content-asset-username": false},
	} {
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": patch}, admin)
		expect(t, r, 422)
	}
	r, next := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"watermark-content": "{user} · {assetUser} · {asset}", "watermark-font-color": "#aabbcc33", "watermark-font-size": 22, "watermark-content-asset-username": true, "watermark-content-user-account": false}}, admin)
	expect(t, r, 202)
	tag = next
	newID, _ := reserve(t, srv, admin, asset, false)
	fresh, e := a.sessions.Get(ctx, newID)
	if e != nil || fresh.Watermark.Revision <= old.Watermark.Revision || fresh.Watermark.Color != "#aabbcc33" || fresh.Watermark.Size != 22 || len(fresh.Watermark.Content) != 2 || !strings.Contains(fresh.Watermark.Content[0], "accept-admin · tester ·") || fresh.Watermark.Content[1] != "资产账户：tester" {
		t.Fatal(fresh.Watermark, e)
	}
	unchanged, e := a.sessions.Get(ctx, id)
	if e != nil || !reflect.DeepEqual(unchanged.Watermark, old.Watermark) {
		t.Fatal("existing snapshot changed", e)
	}
	joined := object(t, call(t, srv.URL, "POST", "/api/sessions/"+id+"/observers", map[string]string{"ticket": ""}, admin, origin))
	raw, _ := json.Marshal(joined["watermark"])
	var observed system.Watermark
	if json.Unmarshal(raw, &observed) != nil || !reflect.DeepEqual(observed, old.Watermark) {
		t.Fatal("observer did not use original snapshot")
	}
	postID(t, srv, "/api/admin/users", userInput("watermark-outsider", nil, nil), admin)
	member := loginAs(t, srv, "watermark-outsider")
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+id, nil, member, origin), 403)
	expect(t, call(t, srv.URL, "POST", "/api/sessions/"+id+"/observers", map[string]string{"ticket": ""}, member, origin), 403)
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"watermark-enabled": false}}, member)
	expect(t, r, 403)
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"watermark-enabled": false}}, admin)
	expect(t, r, 202)
	disabledID, _ := reserve(t, srv, admin, asset, false)
	disabled, e := a.sessions.Get(ctx, disabledID)
	if e != nil || disabled.Watermark.Enabled || len(disabled.Watermark.Content) != 0 {
		t.Fatal(disabled.Watermark, e)
	}
	items, _, e := a.sessions.List(ctx, "", false, 0, 20)
	if e != nil || len(items) != 3 {
		t.Fatal("session listing with snapshots", len(items), e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/sessions/"+id+"/terminate", nil, admin, origin), 204)
	waitClosed(t, a, id)
}
