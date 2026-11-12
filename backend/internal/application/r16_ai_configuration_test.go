package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/system"
	"strings"
	"sync/atomic"
	"testing"
)

func TestR16AIConfigurationDomainProjectionAndExecution(t *testing.T) {
	_, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("ai-config-member", nil, nil), admin)
	member := loginAs(t, srv, "ai-config-member")
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"settings fixture reply"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer model.Close()
	profile := postID(t, srv, "/api/admin/ai/profiles", map[string]any{"name": "independent fixture", "baseUrl": model.URL + "/v1", "model": "fixture", "allowPrivate": true, "enabled": true, "maxOutputTokens": 128, "inputMicrosPerThousand": 1, "outputMicrosPerThousand": 1}, admin)
	current := func() system.Revision {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	original := current()
	settings := map[string]any{"enabled": true, "readonlyEnabled": true, "version": 1, "dailyBudgetMicros": 12345, "maxTurnCostMicros": 321, "commandTimeoutSeconds": 5, "shellEnabled": true, "auditEnabled": true, "shellPrompt": "domain\n\ttool prompt", "auditPrompt": "domain audit prompt"}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", settings, admin, origin), 200)
	v := current()
	if v.ID == original.ID || !v.Values.Bool("llm-enabled") || !v.Values.Bool("llm-shell-enabled") || v.Values.String("llm-shell-prompt") != "domain\n\ttool prompt" {
		t.Fatal("domain not mirrored", v)
	}
	conversation := postID(t, srv, "/api/ai/conversations", map[string]any{"profileId": profile, "title": "global configuration"}, admin)
	message := map[string]any{"text": "hello", "clientMessageId": "87138df5-c51a-40bc-bdc0-645a895f83de"}
	expect(t, call(t, srv.URL, "POST", "/api/ai/conversations/"+conversation+"/messages", message, admin, origin), 200)
	if calls.Load() != 1 {
		t.Fatal("provider not invoked")
	}
	// A multibyte multiline prompt exceeds the old request size, and is kept
	// exactly as entered rather than silently truncated or converted to HTML.
	prompt := strings.Repeat("测试😀\n", 7000)
	body := map[string]any{"set": map[string]any{"llm-enabled": false, "llm-shell-enabled": false, "llm-audit-prompt": prompt}}
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), body, member)
	expect(t, r, 403)
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), body, admin)
	expect(t, r, 202)
	cfg, e := a.aiService().Settings(ctx)
	if e != nil || cfg.Enabled || cfg.ShellEnabled || !cfg.AuditEnabled || !cfg.ReadonlyEnabled || cfg.AuditPrompt != prompt || cfg.Version != 3 || cfg.DailyBudgetMicros != 12345 || cfg.MaxTurnCostMicros != 321 || cfg.CommandTimeoutSeconds != 5 {
		t.Fatal("projection changed independent policy", cfg.Version, e)
	}
	profiles, e := a.aiService().Profiles(ctx, true)
	if e != nil || len(profiles) != 1 || profiles[0].ID != profile || profiles[0].Version != 1 {
		t.Fatal("independent profile changed", e)
	}
	message["clientMessageId"] = "87138df5-c51a-40bc-bdc0-645a895f83df"
	expect(t, call(t, srv.URL, "POST", "/api/ai/conversations/"+conversation+"/messages", message, admin, origin), 403)
	if calls.Load() != 1 {
		t.Fatal("disabled AI contacted provider")
	}
	settings["version"] = 2
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", settings, admin, origin), 409)
	id := current().ID
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(id), map[string]any{"set": map[string]any{"llm-shell-prompt": "bad\x00prompt", "llm-enabled": true}}, admin)
	expect(t, r, 422)
	if current().ID != id {
		t.Fatal("invalid batch partially committed")
	}
	// Recovery appends a matching revision and never leaves the unified view
	// claiming AI execution is enabled in the isolated restored deployment.
	settings["version"] = cfg.Version
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", settings, admin, origin), 200)
	if e = a.db.Within(ctx, func(c context.Context) error { return a.aiService().QuarantineRestore(c) }); e != nil {
		t.Fatal(e)
	}
	restored := current()
	if restored.Values.Bool("llm-enabled") || restored.Values.Bool("llm-shell-enabled") || restored.Values.Bool("llm-audit-enabled") {
		t.Fatal("restore retained AI execution")
	}
	encoded, _ := json.Marshal(restored.Values)
	if strings.Contains(string(encoded), model.URL) {
		t.Fatal("profile leaked into global config")
	}
}
