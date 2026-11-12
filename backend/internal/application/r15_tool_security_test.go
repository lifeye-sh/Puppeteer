package application

import (
	"context"
	"strings"
	"testing"
)

func TestR15ToolApprovalRevokeRejectAndExpiry(t *testing.T) {
	pool, app, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	uid := postID(t, srv, "/api/admin/users", userInput("tool-owner", nil, nil), admin)
	user := loginAs(t, srv, "tool-owner")
	cred := postID(t, srv, "/api/admin/credentials", credInput("toolcred"), admin)
	asset := postID(t, srv, "/api/admin/assets", assetInput("tool scope", cred, ""), admin)
	grant := postID(t, srv, "/api/admin/grants", map[string]any{"userId": uid, "resourceId": asset}, admin)
	p := postID(t, srv, "/api/admin/ai/profiles", map[string]any{"name": "security", "baseUrl": "https://model.invalid", "model": "fixture", "enabled": true, "maxOutputTokens": 64}, admin)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": true, "readonlyEnabled": true, "version": 1}, admin, origin), 200)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/assets/"+asset, map[string]any{"enabled": true, "version": 0}, admin, origin), 200)
	c := postID(t, srv, "/api/ai/conversations", map[string]any{"profileId": p, "title": "security", "contextType": "asset", "contextId": asset}, user)
	base := "/api/ai/conversations/" + c + "/tools"
	propose := func(n int) map[string]any {
		r := call(t, srv.URL, "POST", base, map[string]any{"clientId": taskUUID(n), "name": "asset_inspect", "arguments": map[string]any{}}, user, origin)
		expect(t, r, 200)
		return object(t, r)
	}
	action := func(v map[string]any, decision string) result {
		return call(t, srv.URL, "POST", "/api/ai/tools/"+v["id"].(string)+"/decide", map[string]any{"decision": decision, "parameterHash": v["parameterHash"], "revision": v["revision"]}, user, origin)
	}
	v := propose(15201)
	same := propose(15201)
	if v["id"] != same["id"] {
		t.Fatal("proposal replay duplicated")
	}
	expect(t, call(t, srv.URL, "GET", base, nil, admin, origin), 404)
	expect(t, action(v, "reject"), 200)
	expect(t, call(t, srv.URL, "POST", "/api/ai/tools/"+v["id"].(string)+"/execute", map[string]any{}, user, origin), 409)
	expired := propose(15202)
	_, e := pool.Exec(context.Background(), `UPDATE ai_tool_executions SET expires_at=now()-interval '1 second' WHERE id=$1`, expired["id"])
	if e != nil {
		t.Fatal(e)
	}
	expect(t, action(expired, "approve"), 409)
	if e = app.aiService().RecoverJobs(context.Background()); e != nil {
		t.Fatal(e)
	}
	var state string
	_ = pool.QueryRow(context.Background(), `SELECT state FROM ai_tool_executions WHERE id=$1`, expired["id"]).Scan(&state)
	if state != "timed_out" {
		t.Fatal(state)
	}
	changed := propose(15203)
	_, e = pool.Exec(context.Background(), `UPDATE ai_tool_executions SET arguments=arguments||'{"command":"forged"}'::jsonb WHERE id=$1`, changed["id"])
	if e != nil {
		t.Fatal(e)
	}
	expect(t, action(changed, "approve"), 409)
	cancelled := propose(15204)
	expect(t, action(cancelled, "approve"), 200)
	expect(t, call(t, srv.URL, "POST", "/api/ai/tools/"+cancelled["id"].(string)+"/cancel", map[string]any{}, user, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/ai/tools/"+cancelled["id"].(string)+"/execute", map[string]any{}, user, origin), 409)
	pending := propose(15205)
	expect(t, action(pending, "approve"), 200)
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/grants/"+grant, nil, admin, origin), 204)
	expect(t, call(t, srv.URL, "POST", "/api/ai/tools/"+pending["id"].(string)+"/execute", map[string]any{}, user, origin), 403)
	history := call(t, srv.URL, "GET", base, nil, user, origin)
	expect(t, history, 403)
	if strings.Contains(string(history.body), "forged") {
		t.Fatal("unauthorized history exposed")
	}
}
