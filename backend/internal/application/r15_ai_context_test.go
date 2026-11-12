package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/session"
	"strings"
	"sync/atomic"
	"testing"
)

func TestR15ReadonlyContextConsentScopeAndRedaction(t *testing.T) {
	pool, app, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	uid := postID(t, srv, "/api/admin/users", userInput("context-user", nil, nil), admin)
	user := loginAs(t, srv, "context-user")
	credential := postID(t, srv, "/api/admin/credentials", credInput("private"), admin)
	input := assetInput("Context fixture", credential, "")
	input["description"] = "private-description-never-send"
	asset := postID(t, srv, "/api/admin/assets", input, admin)
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		raw, _ := json.Marshal(body)
		for _, secret := range []string{"remote-only-secret", "private-description-never-send", credential, asset} {
			if strings.Contains(string(raw), secret) {
				t.Error("non-whitelisted field sent")
			}
		}
		if !strings.Contains(string(raw), "192.0.2.10") {
			t.Error("context absent")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"metadata fixture"}}]}`))
	}))
	defer model.Close()
	profile := postID(t, srv, "/api/admin/ai/profiles", map[string]any{"name": "context", "baseUrl": model.URL, "model": "fixture", "allowPrivate": true, "enabled": true, "maxOutputTokens": 64}, admin)
	settings := map[string]any{"enabled": true, "version": 1}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", settings, admin, origin), 200)
	preview := "/api/ai/context?type=asset&targetId=" + asset
	expect(t, call(t, srv.URL, "GET", preview, nil, admin, origin), 403)
	settings["version"] = 2
	settings["readonlyEnabled"] = true
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", settings, admin, origin), 200)
	expect(t, call(t, srv.URL, "GET", preview, nil, admin, origin), 403)
	accessPath := "/api/admin/ai/assets/" + asset
	expect(t, call(t, srv.URL, "PUT", accessPath, map[string]any{"enabled": true, "version": 0}, user, origin), 403)
	expect(t, call(t, srv.URL, "PUT", accessPath, map[string]any{"enabled": true, "version": 0}, admin, origin), 200)
	expect(t, call(t, srv.URL, "PUT", accessPath, map[string]any{"enabled": false, "version": 0}, admin, origin), 409)
	expect(t, call(t, srv.URL, "GET", preview, nil, user, origin), 403)
	grant := postID(t, srv, "/api/admin/grants", map[string]any{"userId": uid, "resourceId": asset}, admin)
	snap := call(t, srv.URL, "GET", preview, nil, user, origin)
	expect(t, snap, 200)
	scope := map[string]any{"profileId": profile, "title": "readonly", "contextType": "asset", "contextId": asset}
	conversation := postID(t, srv, "/api/ai/conversations", scope, user)
	path := "/api/ai/conversations/" + conversation
	msg := map[string]any{"text": "Summarize the asset", "clientMessageId": "a401f918-799c-45f8-a3b3-74c0548a390a"}
	expect(t, call(t, srv.URL, "POST", path+"/messages", msg, user, origin), 409)
	msg["contextDigest"] = "invalid"
	expect(t, call(t, srv.URL, "POST", path+"/messages", msg, user, origin), 409)
	var used int
	if e := pool.QueryRow(context.Background(), `SELECT count(*) FROM ai_usage_ledger`).Scan(&used); e != nil || used != 0 {
		t.Fatal("unconfirmed turn consumed quota", e)
	}
	msg["contextDigest"] = object(t, snap)["digest"]
	expect(t, call(t, srv.URL, "POST", path+"/messages", msg, user, origin), 200)
	if calls.Load() != 1 {
		t.Fatal("unexpected model calls")
	}
	// A known session ID does not confer another user's audit access.
	adminID := object(t, call(t, srv.URL, "GET", "/api/account/info", nil, admin, origin))["id"].(string)
	var sessionID string
	e := app.db.Within(context.Background(), func(ctx context.Context) error {
		var err error
		sessionID, err = app.sessions.Reserve(ctx, session.Session{ActorID: adminID, AssetID: asset, AssetVersion: 1, CredentialVersion: 1}, "fixture-ticket", "fixture-auth")
		return err
	})
	if e != nil {
		t.Fatal(e)
	}
	sessionPreview := "/api/ai/context?type=session&targetId=" + sessionID
	expect(t, call(t, srv.URL, "GET", sessionPreview, nil, user, origin), 403)
	own := call(t, srv.URL, "GET", sessionPreview, nil, admin, origin)
	expect(t, own, 200)
	var snapshot map[string]any
	if e = json.Unmarshal([]byte(object(t, own)["snapshot"].(string)), &snapshot); e != nil {
		t.Fatal(e)
	}
	if len(snapshot["asset"].(map[string]any)) != 4 || len(snapshot["session"].(map[string]any)) != 4 {
		t.Fatal("unexpected context fields", snapshot)
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/grants/"+grant, nil, admin, origin), 204)
	expect(t, call(t, srv.URL, "GET", path, nil, user, origin), 403)
	expect(t, call(t, srv.URL, "POST", path+"/messages", msg, user, origin), 403)
	if calls.Load() != 1 {
		t.Fatal("revoked context reused")
	}
	var auditCount int
	if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM operation_logs WHERE action='ai.context_read'`).Scan(&auditCount); e != nil || auditCount != 1 {
		t.Fatal("missing read audit", e, auditCount)
	}
}
