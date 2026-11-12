package application

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestR16AIRevokedAnswerPreservesKnownUsage(t *testing.T) {
	pool, base, original := r02Server(t, false)
	admin := loginAs(t, original, "accept-admin")
	policy := &aiLicenseFixture{}
	cfg := base.config
	cfg.EntitlementLocal = policy
	app := New(pool, cfg)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy.state.Store(2)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"must be hidden"}}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`)
	}))
	defer model.Close()
	profile := postID(t, srv, "/api/admin/ai/profiles", map[string]any{"name": "usage fixture", "baseUrl": model.URL, "model": "fixture", "allowPrivate": true, "enabled": true, "maxOutputTokens": 64, "inputMicrosPerThousand": 1000, "outputMicrosPerThousand": 1000}, admin)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": true, "version": 1}, admin, origin), 200)
	conversation := postID(t, srv, "/api/ai/conversations", map[string]any{"profileId": profile, "title": "known usage"}, admin)
	expect(t, call(t, srv.URL, "POST", "/api/ai/conversations/"+conversation+"/messages", map[string]any{"text": "hello", "clientMessageId": taskUUID(16690)}, admin, origin), 403)
	var state, reason, output string
	var charge int64
	var known bool
	e := pool.QueryRow(context.Background(), `SELECT t.state,t.reason,t.output,l.charged_micros,l.usage_known FROM ai_turns t JOIN ai_usage_ledger l ON l.turn_id=t.id WHERE t.conversation_id=$1`, conversation).Scan(&state, &reason, &output, &charge, &known)
	if e != nil || state != "failed" || reason != "authorization_changed" || output != "" || charge != 15 || !known {
		t.Fatal("revoked response lost billing evidence", state, reason, output, charge, known, e)
	}
}
