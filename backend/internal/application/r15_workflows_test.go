package application

import (
	"context"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestR15QueueBudgetHistoryPreferencesAndSocket(t *testing.T) {
	pool, app, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"queued reply\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()
	profile := postID(t, srv, "/api/admin/ai/profiles", map[string]any{"name": "queue", "baseUrl": model.URL, "model": "fixture", "enabled": true, "allowPrivate": true, "maxOutputTokens": 64, "inputMicrosPerThousand": 1000, "outputMicrosPerThousand": 1000}, admin)
	settings := map[string]any{"enabled": true, "version": 1, "dailyBudgetMicros": 1}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", settings, admin, origin), 200)
	conversation := postID(t, srv, "/api/ai/conversations", map[string]any{"profileId": profile, "title": "queue"}, admin)
	base := "/api/ai/conversations/" + conversation
	msg := map[string]any{"text": "hello", "clientMessageId": taskUUID(15001)}
	expect(t, call(t, srv.URL, "POST", base+"/messages/queued", msg, admin, origin), 409)
	if calls.Load() != 0 {
		t.Fatal("budget exceeded call")
	}
	settings["dailyBudgetMicros"] = 1000000
	settings["version"] = 2
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", settings, admin, origin), 200)
	q := call(t, srv.URL, "POST", base+"/messages/queued", msg, admin, origin)
	expect(t, q, 200)
	turn := object(t, q)["id"].(string)
	if object(t, q)["state"] != "queued" {
		t.Fatal("not queued")
	}
	expect(t, call(t, srv.URL, "POST", base+"/messages/queued", msg, admin, origin), 200)
	var payload []byte
	if e := pool.QueryRow(context.Background(), `SELECT payload FROM ai_jobs WHERE turn_id=$1`, turn).Scan(&payload); e != nil || strings.Contains(string(payload), admin.Value) {
		t.Fatal("queue payload not encrypted", e)
	}
	// A new worker starts after submission, proving requests are not handler-owned.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); app.RunAIJobs(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(8 * time.Second)
	var state string
	for time.Now().Before(deadline) {
		_ = pool.QueryRow(context.Background(), `SELECT state FROM ai_turns WHERE id=$1`, turn).Scan(&state)
		if state == "succeeded" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if state != "succeeded" || calls.Load() != 1 {
		t.Fatalf("state=%s calls=%d", state, calls.Load())
	}
	var cost int64
	var known bool
	_ = pool.QueryRow(context.Background(), `SELECT charged_micros,usage_known FROM ai_usage_ledger WHERE turn_id=$1`, turn).Scan(&cost, &known)
	if cost != 9 || !known {
		t.Fatal(cost, known)
	}
	expect(t, call(t, srv.URL, "POST", base+"/messages/queued", msg, admin, origin), 200)
	if calls.Load() != 1 {
		t.Fatal("replayed")
	}
	branch := postID(t, srv, base+"/history/branch", map[string]any{"through": turn}, admin)
	if branch == conversation {
		t.Fatal("not branched")
	}
	expect(t, call(t, srv.URL, "POST", base+"/history/compact", map[string]any{"through": turn, "summary": "reviewed summary"}, admin, origin), 200)
	pref := map[string]any{"displayLevel": "detailed", "includeCommandSnippets": true, "defaultProfileId": profile, "version": 0}
	expect(t, call(t, srv.URL, "PUT", "/api/ai/preferences", pref, admin, origin), 200)
	expect(t, call(t, srv.URL, "PUT", "/api/ai/preferences", pref, admin, origin), 409)
	pref["version"] = 1
	expect(t, call(t, srv.URL, "PUT", "/api/ai/preferences", pref, admin, origin), 200)
	if object(t, call(t, srv.URL, "GET", "/api/ai/preferences", nil, admin, origin))["version"] != float64(2) {
		t.Fatal("preference version")
	}
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ai/ws?conversationId=" + conversation
	headers := http.Header{"Origin": []string{origin}, "Cookie": []string{admin.String()}}
	ws, res, e := websocket.DefaultDialer.Dial(wsURL, headers)
	if e != nil {
		t.Fatal(res, e)
	}
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var snapshot map[string]any
	if e = ws.ReadJSON(&snapshot); e != nil {
		t.Fatal(e)
	}
	// Idle sockets must outlive the generic 15-second HTTP request timeout.
	ws.SetReadDeadline(time.Now().Add(22 * time.Second))
	go func() {
		time.Sleep(16 * time.Second)
		_, _ = pool.Exec(context.Background(), `UPDATE ai_turns SET revision=revision+1 WHERE id=$1`, turn)
	}()
	var later map[string]any
	if e = ws.ReadJSON(&later); e != nil {
		t.Fatal("idle websocket disconnected", e)
	}
	if later["type"] != "snapshot" {
		t.Fatal("missing resumed snapshot")
	}
	ws.Close()
	if snapshot["type"] != "snapshot" || len(snapshot["turns"].([]any)) != 1 {
		t.Fatal("snapshot missing")
	}
	headers.Set("Origin", "https://evil.invalid")
	if bad, res, e := websocket.DefaultDialer.Dial(wsURL, headers); e == nil {
		bad.Close()
		t.Fatal("cross origin websocket")
	} else if res == nil || res.StatusCode != 403 {
		t.Fatal("origin result")
	}
	cancel()
	<-done
	// A cancelled queued item never reaches the provider. Unknown in-flight jobs
	// expire to interrupted, retaining the reservation rather than being retried.
	msg["clientMessageId"] = taskUUID(15002)
	q = call(t, srv.URL, "POST", base+"/messages/queued", msg, admin, origin)
	expect(t, q, 200)
	next := object(t, q)["id"].(string)
	expect(t, call(t, srv.URL, "POST", base+"/turns/"+next+"/cancel", map[string]any{}, admin, origin), 200)
	if e = app.aiService().RecoverJobs(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = app.aiService().Claim(context.Background()); e == nil {
		t.Fatal("cancelled job claimed")
	}
	msg["clientMessageId"] = taskUUID(15003)
	q = call(t, srv.URL, "POST", base+"/messages/queued", msg, admin, origin)
	expect(t, q, 200)
	next = object(t, q)["id"].(string)
	if _, e = app.aiService().Claim(context.Background()); e != nil {
		t.Fatal(e)
	}
	_, e = pool.Exec(context.Background(), `UPDATE ai_turns SET deadline=now()-interval '1 second' WHERE id=$1`, next)
	if e != nil {
		t.Fatal(e)
	}
	if e = app.aiService().RecoverJobs(context.Background()); e != nil {
		t.Fatal(e)
	}
	_ = pool.QueryRow(context.Background(), `SELECT state FROM ai_turns WHERE id=$1`, next).Scan(&state)
	if state != "interrupted" || calls.Load() != 1 {
		t.Fatal("unknown call replayed")
	}
}
func TestR15ApprovedToolsRealSFTPAndAudit(t *testing.T) {
	root := t.TempDir()
	path := root + "/edit.txt"
	if e := os.WriteFile(path, []byte("before\n"), 0600); e != nil {
		t.Fatal(e)
	}
	host, port, _ := sshTargetRoot(t, root)
	pool, app, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	session, ticket := reserve(t, srv, admin, asset, false)
	ws := attach(t, srv, admin, session, ticket)
	defer ws.Close()
	readUntil(t, ws, "3"+session)
	profile := postID(t, srv, "/api/admin/ai/profiles", map[string]any{"name": "tools", "baseUrl": "https://model.invalid", "model": "fixture", "enabled": true, "maxOutputTokens": 64}, admin)
	settings := map[string]any{"enabled": true, "readonlyEnabled": true, "shellEnabled": false, "auditEnabled": true, "version": 1}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", settings, admin, origin), 200)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/assets/"+asset, map[string]any{"enabled": true, "version": 0}, admin, origin), 200)
	c := postID(t, srv, "/api/ai/conversations", map[string]any{"profileId": profile, "title": "tools", "contextType": "session", "contextId": session}, admin)
	base := "/api/ai/conversations/" + c + "/tools"
	proposal := map[string]any{"clientId": taskUUID(15100), "name": "session_command", "arguments": map[string]string{"command": "printf fixture"}}
	expect(t, call(t, srv.URL, "POST", base, proposal, admin, origin), 403)
	settings["shellEnabled"] = true
	settings["version"] = 2
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", settings, admin, origin), 200)
	prepare := func(n int, name string, args map[string]string) map[string]any {
		r := call(t, srv.URL, "POST", base, map[string]any{"clientId": taskUUID(n), "name": name, "arguments": args}, admin, origin)
		expect(t, r, 200)
		return object(t, r)
	}
	approve := func(v map[string]any) {
		r := call(t, srv.URL, "POST", "/api/ai/tools/"+v["id"].(string)+"/decide", map[string]any{"decision": "approve", "parameterHash": v["parameterHash"], "revision": v["revision"]}, admin, origin)
		expect(t, r, 200)
	}
	execute := func(v map[string]any) map[string]any {
		r := call(t, srv.URL, "POST", "/api/ai/tools/"+v["id"].(string)+"/execute", map[string]any{}, admin, origin)
		expect(t, r, 200)
		return object(t, r)
	}
	v := prepare(15101, "file_write", map[string]string{"path": path, "content": "after\n"})
	id := v["id"].(string)
	if v["arguments"].(map[string]any)["before"] != "before\n" {
		t.Fatal("diff absent")
	}
	expect(t, call(t, srv.URL, "POST", "/api/ai/tools/"+id+"/execute", map[string]any{}, admin, origin), 409)
	expect(t, call(t, srv.URL, "POST", "/api/ai/tools/"+id+"/decide", map[string]any{"decision": "approve", "parameterHash": "changed", "revision": 1}, admin, origin), 409)
	approve(v)
	expect(t, call(t, srv.URL, "POST", "/api/ai/tools/"+id+"/decide", map[string]any{"decision": "approve", "parameterHash": v["parameterHash"], "revision": 1}, admin, origin), 409)
	result := execute(v)
	if result["state"] != "succeeded" {
		t.Fatal(result)
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != "after\n" {
		t.Fatal("file mutation missing")
	}
	expect(t, call(t, srv.URL, "POST", "/api/ai/tools/"+id+"/execute", map[string]any{}, admin, origin), 409)
	read := prepare(15102, "file_read", map[string]string{"path": path})
	approve(read)
	if !strings.Contains(execute(read)["output"].(string), "after") {
		t.Fatal("read result absent")
	}
	changed := prepare(15103, "file_write", map[string]string{"path": path, "content": "do not overwrite"})
	approve(changed)
	os.WriteFile(path, []byte("concurrent edit"), 0600)
	if execute(changed)["state"] != "needs_revision" {
		t.Fatal("stale file hash accepted")
	}
	actual, _ = os.ReadFile(path)
	if string(actual) != "concurrent edit" {
		t.Fatal("overwrote concurrent edit")
	}
	command := prepare(15104, "session_command", map[string]string{"command": "printf fixture"})
	approve(command)
	out := execute(command)
	if out["state"] != "failed" || !strings.Contains(out["output"].(string), "exitCode") {
		t.Fatal("command exit result missing")
	}
	audit := prepare(15105, "audit_commands", map[string]string{})
	approve(audit)
	if !strings.Contains(execute(audit)["output"].(string), "printf fixture") {
		t.Fatal("audit tool missing command")
	}
	var count int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM session_commands WHERE session_id=$1`, session).Scan(&count)
	if count != 1 {
		t.Fatal("command executed more than once", count)
	}
	pending := prepare(15106, "asset_inspect", map[string]string{})
	approve(pending)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/assets/"+asset, map[string]any{"enabled": false, "version": 1}, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/ai/tools/"+pending["id"].(string)+"/execute", map[string]any{}, admin, origin), 403)
	// Recovered in-flight effects are quarantined, not replayed.
	_, e := pool.Exec(context.Background(), `UPDATE ai_tool_executions SET state='running',started_at=now()-interval '2 minutes' WHERE id=$1`, id)
	if e != nil {
		t.Fatal(e)
	}
	if e = app.aiService().RecoverJobs(context.Background()); e != nil {
		t.Fatal(e)
	}
	var state string
	_ = pool.QueryRow(context.Background(), `SELECT state FROM ai_tool_executions WHERE id=$1`, id).Scan(&state)
	if state != "unknown" {
		t.Fatal(state)
	}
	var leaked bool
	_ = pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM operation_logs WHERE details::text LIKE '%concurrent edit%')`).Scan(&leaked)
	if leaked {
		t.Fatal("file body leaked into audit")
	}
}
