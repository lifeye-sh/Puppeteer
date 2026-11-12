package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestR15ChatIsolationIdempotencyAndSettings(t *testing.T) {
	pool, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("ai-other", nil, nil), admin)
	other := loginAs(t, srv, "ai-other")
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer fixture-only-key" {
			t.Error("provider contract mismatch")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["stream"] != false || body["tools"] != nil {
			t.Error("unexpected tools or streaming")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"fixture reply"}}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`))
	}))
	defer model.Close()
	p := map[string]any{"name": "isolated model", "baseUrl": model.URL + "/v1", "model": "fixture-model", "apiKey": "fixture-only-key", "allowPrivate": true, "enabled": true, "maxOutputTokens": 128}
	expect(t, call(t, srv.URL, "POST", "/api/admin/ai/profiles", p, nil, origin), 401)
	expect(t, call(t, srv.URL, "POST", "/api/admin/ai/profiles", p, other, origin), 403)
	id := postID(t, srv, "/api/admin/ai/profiles", p, admin)
	list := call(t, srv.URL, "GET", "/api/admin/ai/profiles", nil, admin, origin)
	expect(t, list, 200)
	if strings.Contains(string(list.body), "fixture-only-key") || strings.Contains(string(list.body), "apiKey") {
		t.Fatal("key exposed")
	}
	var ciphertext []byte
	if e := pool.QueryRow(context.Background(), `SELECT secret FROM ai_profiles WHERE id=$1`, id).Scan(&ciphertext); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(ciphertext), "fixture-only-key") {
		t.Fatal("key stored in plaintext")
	}
	newConversation := map[string]any{"profileId": id, "title": "验收对话"}
	expect(t, call(t, srv.URL, "POST", "/api/ai/conversations", newConversation, admin, origin), 403)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": true, "version": 1}, admin, origin), 200)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": false, "version": 1}, admin, origin), 409)
	c := postID(t, srv, "/api/ai/conversations", newConversation, admin)
	message := map[string]any{"text": "hello", "clientMessageId": "a401f918-799c-45f8-a3b3-74c0548a390a"}
	path := "/api/ai/conversations/" + c
	expect(t, call(t, srv.URL, "GET", path, nil, other, origin), 404)
	expect(t, call(t, srv.URL, "POST", path+"/messages", message, other, origin), 404)
	r := call(t, srv.URL, "POST", path+"/messages", message, admin, origin)
	expect(t, r, 200)
	if object(t, r)["output"] != "fixture reply" || object(t, r)["state"] != "succeeded" {
		t.Fatalf("wrong result %s", r.body)
	}
	again := call(t, srv.URL, "POST", path+"/messages", message, admin, origin)
	expect(t, again, 200)
	if calls.Load() != 1 || object(t, again)["id"] != object(t, r)["id"] {
		t.Fatal("duplicate request")
	}
	message["text"] = "changed"
	expect(t, call(t, srv.URL, "POST", path+"/messages", message, admin, origin), 409)
	p["version"] = 1
	p["apiKey"] = ""
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/profiles/"+id, p, admin, origin), 200)
	p["version"] = 2
	p["baseUrl"] = model.URL + "/another"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/profiles/"+id, p, admin, origin), 422)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": false, "version": 2}, admin, origin), 200)
	message["clientMessageId"] = "a401f918-799c-45f8-a3b3-74c0548a390b"
	expect(t, call(t, srv.URL, "POST", path+"/messages", message, admin, origin), 403)
	expect(t, call(t, srv.URL, "GET", path, nil, admin, origin), 200)
	expect(t, call(t, srv.URL, "DELETE", path, nil, other, origin), 404)
	expect(t, call(t, srv.URL, "DELETE", path, nil, admin, origin), 200)
	expect(t, call(t, srv.URL, "GET", path, nil, admin, origin), 404)
	// Deleted history must not reset the hourly reservation ledger.
	var used int
	if e := pool.QueryRow(context.Background(), `SELECT count(*) FROM ai_usage_ledger`).Scan(&used); e != nil || used != 1 {
		t.Fatal("usage lost after history deletion", e)
	}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": true, "version": 3}, admin, origin), 200)
	c2 := postID(t, srv, "/api/ai/conversations", newConversation, admin)
	if _, e := pool.Exec(context.Background(), `INSERT INTO ai_usage_ledger(turn_id,user_id) SELECT gen_random_uuid(),user_id FROM ai_conversations,generate_series(1,59) WHERE id=$1`, c2); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/ai/conversations/"+c2+"/messages", message, admin, origin), 409)
	if calls.Load() != 1 {
		t.Fatal("quota bypass")
	}
	var leaked bool
	if e := pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM operation_logs WHERE details::text LIKE '%fixture-only-key%')`).Scan(&leaked); e != nil {
		t.Fatal(e)
	}
	if leaked {
		t.Fatal("key in audit")
	}
}

func TestR15RunningReplayFailureAndReauthorization(t *testing.T) {
	pool, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"discard after revoke"}}]}`))
	}))
	defer model.Close()
	p := postID(t, srv, "/api/admin/ai/profiles", map[string]any{"name": "wait", "baseUrl": model.URL, "model": "test", "allowPrivate": true, "enabled": true, "maxOutputTokens": 64}, admin)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": true, "version": 1}, admin, origin), 200)
	c := postID(t, srv, "/api/ai/conversations", map[string]any{"profileId": p, "title": "wait"}, admin)
	path := "/api/ai/conversations/" + c
	msg := map[string]any{"text": "wait", "clientMessageId": "a401f918-799c-45f8-a3b3-74c0548a390a"}
	done := make(chan result, 1)
	go func() { done <- call(t, srv.URL, "POST", path+"/messages", msg, admin, origin) }()
	<-entered
	r := call(t, srv.URL, "POST", path+"/messages", msg, admin, origin)
	expect(t, r, 200)
	if object(t, r)["state"] != "running" || calls.Load() != 1 {
		t.Fatal("running replay duplicated")
	}
	expect(t, call(t, srv.URL, "DELETE", path, nil, admin, origin), 409)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": false, "version": 2}, admin, origin), 200)
	close(release)
	expect(t, <-done, 403)
	var state, output string
	if e := pool.QueryRow(context.Background(), `SELECT state,output FROM ai_turns WHERE conversation_id=$1`, c).Scan(&state, &output); e != nil || state != "failed" || output != "" {
		t.Fatal("revoked output persisted", e)
	}
	// A dead request is visible as interrupted, and its ID never invokes a model again.
	if _, e := pool.Exec(context.Background(), `UPDATE ai_turns SET state='running',deadline=now()-interval '1 second',reason='' WHERE conversation_id=$1`, c); e != nil {
		t.Fatal(e)
	}
	r = call(t, srv.URL, "POST", path+"/messages", msg, admin, origin)
	expect(t, r, 200)
	if object(t, r)["state"] != "interrupted" || calls.Load() != 1 {
		t.Fatal("interrupted request replayed")
	}
}
