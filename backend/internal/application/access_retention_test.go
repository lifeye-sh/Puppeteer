package application

import (
	"context"
	"testing"
	"time"
)

func TestAccessRetentionSettingsAndCleanup(t *testing.T) {
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	config := object(t, call(t, srv.URL, "GET", "/api/admin/access-retention", nil, admin, origin))
	if config["days"] != float64(31) {
		t.Fatal(config)
	}
	revision := config["revision"]
	expect(t, call(t, srv.URL, "PUT", "/api/admin/access-retention", map[string]any{"days": 0, "revision": 1}, admin, origin), 422)
	postID(t, srv, "/api/admin/users", userInput("retention-denied", nil, nil), admin)
	other := loginAs(t, srv, "retention-denied")
	expect(t, call(t, srv.URL, "PUT", "/api/admin/access-retention", map[string]any{"days": 1, "revision": 1}, other, origin), 403)
	host, port, _ := sshTarget(t)
	asset := r04Asset(t, srv, admin, host, port, true)
	ctx := context.Background()
	old, _ := reserve(t, srv, admin, asset, false)
	recent, _ := reserve(t, srv, admin, asset, false)
	active, _ := reserve(t, srv, admin, asset, false)
	for _, id := range []string{old, recent} {
		if e := a.audit.Record(ctx, id, "output", 1000, []byte("retention test")); e != nil {
			t.Fatal(e)
		}
		a.finishSession(id, "closed", "test")
	}
	if _, e := p.Exec(ctx, `UPDATE sessions SET closed_at=now()-interval '32 days' WHERE id=$1`, old); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `UPDATE sessions SET closed_at=now()-interval '30 days' WHERE id=$1`, recent); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `UPDATE sessions SET created_at=now()-interval '100 days' WHERE id=$1`, active); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+active+"/recording/download", nil, admin, origin), 409)
	// An active export must prevent its chunks from being removed mid-download.
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `SELECT session_id FROM recordings WHERE session_id=$1 FOR SHARE`, old); e != nil {
		t.Fatal(e)
	}
	deadline, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	e = a.CleanupAccessRecords(deadline)
	cancel()
	tx.Rollback(ctx)
	if e == nil {
		t.Fatal("cleanup ignored recording export lock")
	}
	if e = a.CleanupAccessRecords(ctx); e != nil {
		t.Fatal(e)
	}
	// A cancelled PostgreSQL statement may release its row lock asynchronously;
	// SKIP LOCKED intentionally leaves that row for the next maintenance pass.
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); {
		if _, err := a.sessions.Get(ctx, old); err != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
		if err := a.CleanupAccessRecords(ctx); err != nil {
			t.Fatal(err)
		}
	}
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+old, nil, admin, origin), 404)
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+old+"/recording/download", nil, admin, origin), 404)
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+recent, nil, admin, origin), 200)
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+active, nil, admin, origin), 200)
	for _, table := range []string{"access_logs", "recordings", "recording_chunks", "session_commands"} {
		var count int
		if e = p.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE session_id=$1`, old).Scan(&count); e != nil || count != 0 {
			t.Fatal(table, count, e)
		}
	}
	list := object(t, call(t, srv.URL, "GET", "/api/sessions", nil, admin, origin))
	if list["total"] != float64(2) {
		t.Fatal(list)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/audit-retention/preview", map[string]any{"revision": revision, "days": map[string]int{"access-log-saved-limit-days": 7}}, admin, origin), 200)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/access-retention", map[string]any{"days": 7, "revision": revision}, admin, origin), 204)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/access-retention", map[string]any{"days": 90, "revision": revision}, admin, origin), 409)
	if e = a.CleanupAccessRecords(ctx); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+recent, nil, admin, origin), 404)
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+active, nil, admin, origin), 200)
}
