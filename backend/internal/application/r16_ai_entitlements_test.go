package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/ai"
	"next-terminal/backend/internal/modules/entitlements"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type aiLicenseFixture struct{ state atomic.Int32 }

func (p *aiLicenseFixture) Snapshot(instance string, now time.Time) (entitlements.Snapshot, error) {
	v := entitlements.Snapshot{InstanceID: instance, Source: "local", State: "valid", Features: map[string]bool{}}
	switch p.state.Load() {
	case 0:
		v.AllowImplemented = true
	case 2:
		v.State = "expired"
		return v, entitlements.ErrExpired
	case 3:
		v.State = "invalid"
		return v, entitlements.ErrInvalid
	}
	return v, nil
}

func TestR16AIEntitlementAdmissionAndQueuedRevocation(t *testing.T) {
	pool, base, original := r02Server(t, false)
	admin := loginAs(t, original, "accept-admin")
	ctx := context.Background()
	policy := &aiLicenseFixture{}
	cfg := base.config
	cfg.EntitlementLocal = policy
	app := New(pool, cfg)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"allowed response"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer model.Close()
	profile := postID(t, srv, "/api/admin/ai/profiles", map[string]any{"name": "entitlement fixture", "baseUrl": model.URL, "model": "fixture", "allowPrivate": true, "enabled": true, "maxOutputTokens": 64, "inputMicrosPerThousand": 1000, "outputMicrosPerThousand": 1000}, admin)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": true, "version": 1}, admin, origin), 200)
	conversation := postID(t, srv, "/api/ai/conversations", map[string]any{"profileId": profile, "title": "license"}, admin)
	path := "/api/ai/conversations/" + conversation
	msg := map[string]any{"text": "hello", "clientMessageId": taskUUID(16600)}
	complete := call(t, srv.URL, "POST", path+"/messages", msg, admin, origin)
	expect(t, complete, 200)
	queuedMsg := map[string]any{"text": "queued", "clientMessageId": taskUUID(16601)}
	queued := call(t, srv.URL, "POST", path+"/messages/queued", queuedMsg, admin, origin)
	expect(t, queued, 200)
	turn := object(t, queued)["id"].(string)
	user, e := app.identity.FindByName(ctx, "accept-admin")
	if e != nil {
		t.Fatal(e)
	}
	s := app.aiService()
	var tool ai.Execution
	e = app.db.Within(ctx, func(c context.Context) error {
		var err error
		tool, err = s.Propose(c, ai.Execution{UserID: user.ID, ConversationID: conversation, ClientID: taskUUID(16602), Name: "asset_inspect"})
		return err
	})
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []int32{1, 2, 3} {
		policy.state.Store(mode)
		deniedStatus := 403
		if mode == 3 {
			deniedStatus = 422
		}
		expect(t, call(t, srv.URL, "POST", "/api/ai/conversations", map[string]any{"profileId": profile, "title": "blocked"}, admin, origin), deniedStatus)
		for _, suffix := range []string{"messages", "messages/queued", "messages/stream"} {
			expect(t, call(t, srv.URL, "POST", path+"/"+suffix, map[string]any{"text": "denied", "clientMessageId": taskUUID(16610 + int(mode))}, admin, origin), deniedStatus)
		}
		expect(t, call(t, srv.URL, "GET", "/api/admin/ai/profiles/"+profile+"/models", nil, admin, origin), deniedStatus)
		for _, action := range []string{"decide", "execute"} {
			expect(t, call(t, srv.URL, "POST", "/api/ai/tools/"+tool.ID+"/"+action, map[string]any{"decision": "approve", "parameterHash": tool.ParameterHash, "revision": tool.Revision}, admin, origin), deniedStatus)
		}
		// Internal callers cannot bypass the domain boundary.
		if e := s.ClaimExecution(ctx, user.ID, tool.ID); !errors.Is(e, ai.ErrExecutionUnavailable) {
			t.Fatal(e)
		}
		if _, e := s.Propose(ctx, ai.Execution{}); !errors.Is(e, ai.ErrExecutionUnavailable) {
			t.Fatal(e)
		}
		if e := s.Enqueue(ctx, ai.Job{}); !errors.Is(e, ai.ErrExecutionUnavailable) {
			t.Fatal(e)
		}
		if e := s.Decide(ctx, user.ID, tool.ID, tool.ParameterHash, "approve", tool.Revision); !errors.Is(e, ai.ErrExecutionUnavailable) {
			t.Fatal(e)
		}
		// Reading/replaying an existing response never makes a new provider call.
		expect(t, call(t, srv.URL, "POST", path+"/messages", msg, admin, origin), 200)
		for _, url := range []string{path, path + "/tools", "/api/ai/conversations", "/api/ai/usage", "/api/ai/status", "/api/admin/ai/profiles"} {
			expect(t, call(t, srv.URL, "GET", url, nil, admin, origin), 200)
		}
		caps := call(t, srv.URL, "GET", "/api/system/capabilities", nil, admin, origin)
		expect(t, caps, 200)
		f := object(t, caps)["features"].(map[string]any)
		if f["ai.chat"].(map[string]any)["state"] != "available" || f["ai.inference"].(map[string]any)["state"] != "not_entitled" {
			t.Fatal("workspace inaccessible or inference advertised")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("denied requests reached model", calls.Load())
	}
	var rows int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM ai_turns WHERE conversation_id=$1`, conversation).Scan(&rows); e != nil || rows != 2 {
		t.Fatal(rows, e)
	}
	// Worker claims/finalizes revoked queued work, without holding budget forever.
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); app.RunAIJobs(workerCtx) }()
	defer func() { stop(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	var state, reason string
	for time.Now().Before(deadline) {
		_ = pool.QueryRow(ctx, `SELECT state,reason FROM ai_turns WHERE id=$1`, turn).Scan(&state, &reason)
		if state == "failed" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if state != "failed" || reason != "authorization_changed" || calls.Load() != 1 {
		t.Fatal(state, reason, calls.Load())
	}
	var audited bool
	if e = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM operation_logs WHERE action='ai.turn_finished' AND details->>'turnId'=$1 AND details->>'dispatch'='not_started')`, turn).Scan(&audited); e != nil || !audited {
		t.Fatal("queued rejection missing audit", e)
	}
	var charge int64
	var known bool
	var payload int
	if e = pool.QueryRow(ctx, `SELECT charged_micros,usage_known,(SELECT octet_length(payload) FROM ai_jobs WHERE turn_id=$1) FROM ai_usage_ledger WHERE turn_id=$1`, turn).Scan(&charge, &known, &payload); e != nil || charge != 0 || !known || payload != 0 {
		t.Fatal("undispatched job retained budget or auth payload", charge, known, payload, e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/ai/tools/"+tool.ID+"/decide", map[string]any{"decision": "reject", "parameterHash": tool.ParameterHash, "revision": tool.Revision}, admin, origin), 200)
	expect(t, call(t, srv.URL, "DELETE", path, nil, admin, origin), 200)
}

func TestR16AIEntitlementCancelsInflightProvider(t *testing.T) {
	for _, mode := range []string{"sync", "stream", "models"} {
		t.Run(mode, func(t *testing.T) {
			pool, base, original := r02Server(t, false)
			admin := loginAs(t, original, "accept-admin")
			policy := &aiLicenseFixture{}
			cfg := base.config
			cfg.EntitlementLocal = policy
			app := New(pool, cfg)
			srv := httptest.NewServer(app.Handler())
			defer srv.Close()
			entered, aborted := make(chan struct{}, 1), make(chan struct{}, 1)
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					var v any
					_ = json.NewDecoder(r.Body).Decode(&v)
				}
				entered <- struct{}{}
				if mode == "stream" {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
					aborted <- struct{}{}
				case <-time.After(8 * time.Second):
					t.Error("provider not cancelled")
				}
			}))
			defer model.Close()
			profile := postID(t, srv, "/api/admin/ai/profiles", map[string]any{"name": "inflight", "baseUrl": model.URL, "model": "fixture", "allowPrivate": true, "enabled": true, "maxOutputTokens": 64}, admin)
			expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": true, "version": 1}, admin, origin), 200)
			conversation := postID(t, srv, "/api/ai/conversations", map[string]any{"profileId": profile, "title": "inflight"}, admin)
			resultCh := make(chan result, 1)
			go func() {
				if mode == "models" {
					resultCh <- call(t, srv.URL, "GET", "/api/admin/ai/profiles/"+profile+"/models", nil, admin, origin)
					return
				}
				suffix := "/messages"
				if mode == "stream" {
					suffix += "/stream"
				}
				resultCh <- call(t, srv.URL, "POST", "/api/ai/conversations/"+conversation+suffix, map[string]any{"text": "hello", "clientMessageId": taskUUID(16680)}, admin, origin)
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("provider not reached")
			}
			policy.state.Store(2)
			select {
			case <-aborted:
			case <-time.After(4 * time.Second):
				t.Fatal("expired license did not cancel transport")
			}
			var r result
			select {
			case r = <-resultCh:
			case <-time.After(4 * time.Second):
				t.Fatal("request not finalized")
			}
			if mode == "stream" {
				expect(t, r, 200)
				if !strings.Contains(string(r.body), "authorization_changed") {
					t.Fatal(string(r.body))
				}
			} else {
				expect(t, r, 403)
			}
			if mode != "models" {
				var state, reason, output string
				e := pool.QueryRow(context.Background(), `SELECT state,reason,output FROM ai_turns WHERE conversation_id=$1`, conversation).Scan(&state, &reason, &output)
				if e != nil || state != "failed" || reason != "authorization_changed" || output != "" {
					t.Fatal(state, reason, output, e)
				}
			}
		})
	}
}
