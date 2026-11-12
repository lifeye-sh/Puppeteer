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

func TestR16ManagedModelSecretTransportAndIsolation(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer isolated-managed-model-key" {
			t.Error("managed secret not decrypted correctly")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["temperature"] != 0.7 || body["model"] != "managed-fixture" {
			t.Error("managed model parameters lost")
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"managed fixture"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer model.Close()
	independent := postID(t, srv, "/api/admin/ai/profiles", map[string]any{"name": "independent model", "baseUrl": model.URL + "/v1", "model": "independent-fixture", "allowPrivate": true, "enabled": false, "maxOutputTokens": 128}, admin)
	current := func() system.Revision {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	initial := current()
	patch := map[string]any{"llm-enabled": true, "llm-profile-enabled": true, "llm-base-url": model.URL + "/v1", "llm-model": "managed-fixture", "llm-api-key": "isolated-managed-model-key", "llm-temperature": 0.7, "llm-max-tokens": 128}
	save := func(set map[string]any, password string, clear []string, tag string) result {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": set, "password": password, "clearSecrets": clear}, admin)
		return r
	}
	expect(t, save(patch, "", nil, configTag(initial.ID)), 422)
	expect(t, save(patch, testPassword, nil, configTag(initial.ID)), 422) // HTTP requires explicit private opt-in.
	patch["llm-allow-private"] = true
	expect(t, save(patch, testPassword, nil, configTag(initial.ID)), 202)
	created := current()
	ref := created.Values.String("llm-api-key")
	var ciphertext []byte
	if e := pool.QueryRow(ctx, `SELECT sealed FROM configuration_secrets WHERE id=$1`, ref).Scan(&ciphertext); e != nil || strings.Contains(string(ciphertext), "isolated-managed-model-key") {
		t.Fatal("secret not sealed", e)
	}
	profiles, e := a.aiService().Profiles(ctx, true)
	if e != nil || len(profiles) != 2 {
		t.Fatal(e, len(profiles))
	}
	var managed string
	for _, p := range profiles {
		if p.Managed {
			managed = p.ID
			if !p.HasKey || p.Temperature == nil || *p.Temperature != 0.7 {
				t.Fatal("managed metadata incorrect")
			}
		} else if p.ID != independent || p.Version != 1 {
			t.Fatal("independent model changed")
		}
	}
	if managed == "" {
		t.Fatal("managed model missing")
	}
	for _, path := range []string{"/api/settings", "/api/settings/revisions", "/api/admin/properties", "/api/admin/ai/profiles"} {
		r := call(t, srv.URL, "GET", path, nil, admin, origin)
		expect(t, r, 200)
		if strings.Contains(string(r.body), ref) || strings.Contains(string(r.body), "isolated-managed-model-key") {
			t.Fatal("configuration exposed secret", path)
		}
	}
	conversation := postID(t, srv, "/api/ai/conversations", map[string]any{"profileId": managed, "title": "managed model acceptance"}, admin)
	expect(t, call(t, srv.URL, "POST", "/api/ai/conversations/"+conversation+"/messages", map[string]string{"text": "hello", "clientMessageId": "7171bbfc-0571-437f-a1cb-91bcb1ac1334"}, admin, origin), 200)
	if calls.Load() != 1 {
		t.Fatal("no provider request")
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/ai/profiles/"+managed, nil, admin, origin), 409)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/profiles/"+managed, map[string]any{"name": "bypass", "baseUrl": model.URL + "/v1", "model": "managed-fixture", "allowPrivate": true, "enabled": true, "maxOutputTokens": 128, "version": 1}, admin, origin), 409)
	expect(t, save(map[string]any{"llm-temperature": "0.35", "llm-api-key": "*****"}, "", nil, configTag(created.ID)), 202)
	updated := current()
	if updated.Values.Float("llm-temperature") != 0.35 || updated.Values.String("llm-api-key") != ref {
		t.Fatal("legacy decimal/mask failed")
	}
	for _, bad := range []any{-0.5, -2, 2.1, "NaN", "Inf", true, nil} {
		expect(t, save(map[string]any{"llm-temperature": bad}, "", nil, configTag(updated.ID)), 422)
	}
	// Repointing with the stored secret is never allowed, even for an admin.
	expect(t, save(map[string]any{"llm-base-url": model.URL + "/other"}, "", nil, configTag(updated.ID)), 422)
	expect(t, save(map[string]any{"llm-proxy-url": "http://user:password@127.0.0.1:9000"}, "", nil, configTag(updated.ID)), 422)
	expect(t, save(map[string]any{"llm-api-key": "isolated-second-key"}, testPassword, nil, configTag(initial.ID)), 409)
	var secretCount int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM configuration_secrets WHERE key='llm-api-key'`).Scan(&secretCount); e != nil || secretCount != 1 {
		t.Fatal("stale mutation leaked a secret row", secretCount, e)
	}
	expect(t, save(map[string]any{"llm-base-url": model.URL + "/other", "llm-profile-enabled": false}, testPassword, []string{"llm-api-key"}, configTag(updated.ID)), 202)
	p, e := a.aiService().Provider(ctx, managed)
	if e == nil && p.APIKey != "" {
		t.Fatal("clear kept decrypted secret")
	}
	if current().Values.String("llm-api-key") != "" {
		t.Fatal("clear reference retained")
	}
	// Isolated recovery disables the managed profile as well as global tools.
	expect(t, save(map[string]any{"llm-profile-enabled": true}, "", nil, configTag(current().ID)), 202)
	if e = a.aiService().QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	restored := current()
	if restored.Values.Bool("llm-profile-enabled") || restored.Values.Bool("llm-enabled") {
		t.Fatal("restored model remains enabled")
	}
	profiles, e = a.aiService().Profiles(ctx, true)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range profiles {
		if p.Managed && p.Enabled {
			t.Fatal("managed projection not quarantined")
		}
		if !p.Managed && p.Version != 1 {
			t.Fatal("independent profile mutated")
		}
	}
}
