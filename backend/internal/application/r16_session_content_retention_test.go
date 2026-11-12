package application

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/modules/system"
	"testing"
	"time"
)

func TestR16SessionContentRetentionPreservesSummaryAndHold(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	host, port, _ := sshTarget(t)
	asset := r04Asset(t, srv, admin, host, port, true)
	var actor string
	if e := pool.QueryRow(ctx, `SELECT id FROM users WHERE username='accept-admin'`).Scan(&actor); e != nil {
		t.Fatal(e)
	}
	head := func() system.Revision {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	save := func(days map[string]int, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(head().ID), map[string]any{"set": days}, admin)
		expect(t, r, want)
	}
	if head().Values.Int("session-saved-limit-days") != 31 {
		t.Fatal("default must preserve 31-day access requirement")
	}
	save(map[string]int{"access-log-saved-limit-days": 90}, 202)
	create := func() string {
		t.Helper()
		id, _ := reserve(t, srv, admin, asset, false)
		if e := a.audit.Record(ctx, id, "output", 1000, []byte("retained terminal output")); e != nil {
			t.Fatal(e)
		}
		if _, e := a.audit.StartCommand(ctx, id, actor, "fixture command"); e != nil {
			t.Fatal(e)
		}
		a.finishSession(id, "closed", "fixture")
		if _, e := pool.Exec(ctx, `UPDATE sessions SET closed_at=now()-interval '7 days' WHERE id=$1`, id); e != nil {
			t.Fatal(e)
		}
		return id
	}
	expired, held, exporting := create(), create(), create()
	active, _ := reserve(t, srv, admin, asset, false)
	if _, e := pool.Exec(ctx, `UPDATE sessions SET created_at=now()-interval '7 days' WHERE id=$1`, active); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/audit-holds", map[string]any{"category": "access", "recordId": held, "reason": "retain session evidence", "held": true}, admin, origin), 204)
	days := map[string]int{"session-saved-limit-days": 1}
	save(days, 409)
	preview := object(t, call(t, srv.URL, "POST", "/api/admin/audit-retention/preview", map[string]any{"revision": head().ID, "days": days}, admin, origin))
	items := preview["items"].([]any)
	item := items[0].(map[string]any)
	if len(items) != 1 || item["eligible"] != float64(2) || item["held"] != float64(1) {
		t.Fatal(preview)
	}
	export, e := a.audit.RequestExport(ctx, expired, actor)
	if e != nil {
		t.Fatal(e)
	}
	save(days, 202)
	// A complete download snapshot holds the recording until its reader exits.
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var lock string
	if e = tx.QueryRow(ctx, `SELECT session_id FROM recordings WHERE session_id=$1 FOR SHARE`, exporting).Scan(&lock); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		done <- a.CleanupAccessRecords(c)
	}()
	select {
	case e := <-done:
		tx.Rollback(ctx)
		t.Fatal("purged while export locked", e)
	case <-time.After(100 * time.Millisecond):
	}
	tx.Rollback(ctx)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{expired, exporting} {
		access, e := a.audit.Access(ctx, id)
		if e != nil || access.RecordingState != "expired" || access.RecordingBytes != 0 {
			t.Fatal(access, e)
		}
		commands, e := a.audit.Commands(ctx, id)
		if e != nil || len(commands) != 0 {
			t.Fatal("commands retained", e)
		}
		if _, e = a.sessions.Get(ctx, id); e != nil {
			t.Fatal("summary disappeared", e)
		}
		expect(t, call(t, srv.URL, "GET", "/api/sessions/"+id+"/recording", nil, admin, origin), 409)
	}
	var cancelled bool
	if e = pool.QueryRow(ctx, `SELECT cancel_requested FROM jobs WHERE id=$1`, export.ID).Scan(&cancelled); e != nil || !cancelled {
		t.Fatal("conversion not cancelled", e)
	}
	v, e := a.audit.Access(ctx, held)
	if e != nil || v.RecordingBytes == 0 {
		t.Fatal("held payload deleted", v, e)
	}
	v, e = a.audit.Access(ctx, active)
	if e != nil || v.RecordingState != "writing" {
		t.Fatal("active session expired", v, e)
	}
	// Releasing the common parent hold permits payload-only cleanup.
	expect(t, call(t, srv.URL, "POST", "/api/admin/audit-holds", map[string]any{"category": "access", "recordId": held, "reason": "review completed", "held": false}, admin, origin), 204)
	if e = a.CleanupAccessRecords(ctx); e != nil {
		t.Fatal(e)
	}
	v, e = a.audit.Access(ctx, held)
	if e != nil || v.RecordingState != "expired" {
		t.Fatal(v, e)
	}
	// No early payload cleanup (-1) does not outlive its parent access record.
	save(map[string]int{"session-saved-limit-days": -1}, 202)
	parent := map[string]int{"access-log-saved-limit-days": 1}
	expect(t, call(t, srv.URL, "POST", "/api/admin/audit-retention/preview", map[string]any{"revision": head().ID, "days": parent}, admin, origin), 200)
	save(parent, 202)
	if e = a.CleanupAccessRecords(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = a.audit.Access(ctx, held); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("parent access still visible", e)
	}
}
