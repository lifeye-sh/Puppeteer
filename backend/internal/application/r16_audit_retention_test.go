package application

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/modules/automation"
	"next-terminal/backend/internal/modules/pki"
	"next-terminal/backend/internal/modules/system"
	"sync"
	"testing"
)

func TestR16CategorizedRetentionAndHolds(t *testing.T) {
	p, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("retention-member", nil, nil), admin)
	member := loginAs(t, srv, "retention-member")
	for _, path := range []string{"/api/admin/audit-retention", "/api/admin/audit-holds"} {
		expect(t, call(t, srv.URL, "GET", path, nil, member, origin), 403)
	}
	rev, e := a.system.Configuration(ctx, 0)
	if e != nil {
		t.Fatal(e)
	}
	// Session payload has a dependent parent lifecycle, covered separately.
	categories := []system.RetentionCategory{}
	for _, c := range system.RetentionCategories() {
		if c.ID != "session" {
			categories = append(categories, c)
		}
	}
	proposed := map[string]int{}
	for _, c := range categories {
		proposed[c.Key] = 1
		if c.ID != "access" && rev.Values.Int(c.Key) != -1 {
			t.Fatal("upgrade must not silently start deleting", c.ID)
		}
	}
	patch := map[string]any{"set": proposed}
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(rev.ID), patch, admin)
	expect(t, r, 409)
	expect(t, call(t, srv.URL, "POST", "/api/admin/audit-retention/preview", map[string]any{"revision": rev.ID, "days": map[string]int{"login-log-saved-limit-days": 0}}, admin, origin), 422)
	host, port, _ := sshTarget(t)
	asset := r04Asset(t, srv, admin, host, port, true)
	var actor string
	if e = p.QueryRow(ctx, `SELECT id FROM users WHERE username='accept-admin'`).Scan(&actor); e != nil {
		t.Fatal(e)
	}
	credential := postID(t, srv, "/api/admin/credentials", credInput("retention-db"), admin)
	ca, key, e := pki.Generate("retention db", []string{"db.internal"}, 2, true, nil)
	clear(key)
	if e != nil {
		t.Fatal(e)
	}
	dbAsset := postID(t, srv, "/api/admin/database-assets", map[string]any{"name": "Retention DB", "alias": "retention_db", "host": "db.internal", "port": 3306, "database": "inventory", "readonlyCredentialId": credential, "tlsServerName": "db.internal", "tlsCa": string(ca), "enabled": true, "connectTimeout": 10}, admin)
	var dbSession string
	if e = p.QueryRow(ctx, `INSERT INTO database_sessions(user_id,asset_id,password_version,asset_version,remote_address) VALUES($1,$2,1,1,'fixture') RETURNING id`, actor, dbAsset).Scan(&dbSession); e != nil {
		t.Fatal(e)
	}
	old := map[string][]string{}
	for _, c := range categories {
		for i := 0; i < 2; i++ {
			var id string
			switch c.ID {
			case "access":
				id, _ = reserve(t, srv, admin, asset, false)
				a.finishSession(id, "closed", "retention-fixture")
				_, e = p.Exec(ctx, `UPDATE sessions SET closed_at=now()-interval '40 days' WHERE id=$1`, id)
			case "login":
				e = p.QueryRow(ctx, `INSERT INTO login_logs(actor_id,outcome,request_id,source_ip,created_at) VALUES($1,'succeeded','r16-retention','',now()-interval '40 days') RETURNING id`, actor).Scan(&id)
			case "operation":
				e = p.QueryRow(ctx, `INSERT INTO operation_logs(actor_id,action,outcome,request_id,source_ip,created_at) VALUES($1,'fixture','succeeded','r16-retention','',now()-interval '40 days') RETURNING id`, actor).Scan(&id)
			case "sql":
				e = p.QueryRow(ctx, `INSERT INTO database_sql_logs(session_id,sql_text,sql_sha256,command,state,created_at,finished_at) VALUES($1,'SELECT 1','fixture',3,'completed',now()-interval '40 days',now()-interval '40 days') RETURNING id`, dbSession).Scan(&id)
			case "event":
				e = p.QueryRow(ctx, `INSERT INTO system_events(rule_name,metric,kind,created_at) VALUES('fixture','agent.cpu','firing',now()-interval '40 days') RETURNING id`).Scan(&id)
			case "filesystem":
				e = p.QueryRow(ctx, `INSERT INTO filesystem_logs(session_id,actor_id,action,path,state,ended_at) VALUES($1,$2,'list','/fixture','succeeded',now()-interval '40 days') RETURNING id`, old["access"][0], actor).Scan(&id)
			case "cron":
				var schedule string
				e = p.QueryRow(ctx, `INSERT INTO schedules(owner_id,name,kind,command,asset_ids,timeout_seconds,source_ip) VALUES($1,'retention-fixture','ssh.command','echo fixture',ARRAY[$2::uuid],10,'') RETURNING id`, actor, asset).Scan(&schedule)
				if e == nil {
					e = p.QueryRow(ctx, `INSERT INTO jobs(kind,state,ended_at) VALUES('ssh.command','succeeded',now()-interval '40 days') RETURNING id`).Scan(&id)
				}
				if e == nil {
					_, e = p.Exec(ctx, `INSERT INTO scheduled_runs(id,schedule_id,actor_id,trigger_key,snapshot) VALUES($1,$2,$3,'fixture','{"command":"sensitive fixture output"}')`, id, schedule, actor)
				}
				if e == nil {
					_, e = p.Exec(ctx, `INSERT INTO scheduled_run_targets(run_id,asset_id,state,output) VALUES($1,$2,'succeeded','retained only while allowed')`, id, asset)
				}
			}
			if e != nil {
				t.Fatal(c.ID, e)
			}
			old[c.ID] = append(old[c.ID], id)
			if i == 1 {
				expect(t, call(t, srv.URL, "POST", "/api/admin/audit-holds", map[string]any{"category": c.ID, "recordId": id, "reason": "retention acceptance hold", "held": true}, admin, origin), 204)
			}
		}
	}
	result := object(t, call(t, srv.URL, "POST", "/api/admin/audit-retention/preview", map[string]any{"revision": rev.ID, "days": proposed}, admin, origin))
	for _, item := range result["items"].([]any) {
		v := item.(map[string]any)
		if v["eligible"] != float64(1) || v["held"] != float64(1) {
			t.Fatal("preview counts", v)
		}
	}
	// A preview must match the exact proposed days; changed days and expired previews cannot authorize shortening.
	changed := map[string]any{"set": map[string]int{"login-log-saved-limit-days": 2}}
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(rev.ID), changed, admin)
	expect(t, r, 409)
	if _, e = p.Exec(ctx, `UPDATE audit_retention_previews SET expires_at=now()-interval '1 second'`); e != nil {
		t.Fatal(e)
	}
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(rev.ID), patch, admin)
	expect(t, r, 409)
	expect(t, call(t, srv.URL, "POST", "/api/admin/audit-retention/preview", map[string]any{"revision": rev.ID, "days": proposed}, admin, origin), 200)
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(rev.ID), patch, admin)
	expect(t, r, 202)
	// Running SQL and file operations must survive even with old timestamps.
	var activeSQL string
	if e = p.QueryRow(ctx, `INSERT INTO database_sql_logs(session_id,sql_text,sql_sha256,command,created_at) VALUES($1,'SELECT 1','active',3,now()-interval '40 days') RETURNING id`, dbSession).Scan(&activeSQL); e != nil {
		t.Fatal(e)
	}
	if e = a.CleanupAccessRecords(ctx); e != nil {
		t.Fatal(e)
	}
	tables := map[string]string{"access": "sessions", "login": "login_logs", "cron": "scheduled_runs", "sql": "database_sql_logs", "operation": "operation_logs", "filesystem": "filesystem_logs", "event": "system_events"}
	for category, ids := range old {
		for i, id := range ids {
			var exists bool
			extra := ""
			if category == "access" || category == "cron" {
				extra = " AND audit_purged_at IS NULL"
			}
			if e = p.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+tables[category]+` WHERE id=$1`+extra+`)`, id).Scan(&exists); e != nil || exists != (i == 1) {
				t.Fatal(category, i, exists, e)
			}
		}
	}
	var count int
	if e = p.QueryRow(ctx, `SELECT count(*) FROM database_sql_logs WHERE id=$1`, activeSQL).Scan(&count); e != nil || count != 1 {
		t.Fatal("active SQL deleted", e)
	}
	if _, e = automation.New(a.db).Run(ctx, old["cron"][0]); e == nil {
		t.Fatal("purged run remains visible")
	}
	var snapshot string
	if e = p.QueryRow(ctx, `SELECT snapshot::text FROM scheduled_runs WHERE id=$1`, old["cron"][0]).Scan(&snapshot); e != nil || snapshot != "{}" {
		t.Fatal("must keep tombstone without payload", snapshot, e)
	}
	// Permanently retained access audit survives later maintenance.
	now, e := a.system.Configuration(ctx, 0)
	if e != nil {
		t.Fatal(e)
	}
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(now.ID), map[string]any{"set": map[string]int{"access-log-saved-limit-days": -1}}, admin)
	expect(t, r, 202)
	for category, ids := range old {
		expect(t, call(t, srv.URL, "POST", "/api/admin/audit-holds", map[string]any{"category": category, "recordId": ids[1], "reason": "acceptance complete", "held": false}, admin, origin), 204)
	}
	if e = a.CleanupAccessRecords(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = a.sessions.Get(ctx, old["access"][1]); e != nil {
		t.Fatal("forever access removed", e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/audit-holds", map[string]any{"category": "login", "recordId": old["login"][0], "reason": "too late", "held": true}, admin, origin), 404)
	expect(t, call(t, srv.URL, "POST", "/api/admin/audit-holds", map[string]any{"category": "access", "recordId": old["access"][1], "reason": "", "held": true}, admin, origin), 422)
	list := object(t, call(t, srv.URL, "GET", "/api/admin/audit-holds", nil, admin, origin))
	if list["total"] != float64(0) {
		t.Fatal(list)
	}
	// Stale versions never authorize a later settings batch.
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(rev.ID), patch, admin)
	expect(t, r, 409)
}

func TestR16HoldCleanupRace(t *testing.T) {
	p, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	var actor string
	if e := p.QueryRow(ctx, `SELECT id FROM users WHERE username='accept-admin'`).Scan(&actor); e != nil {
		t.Fatal(e)
	}
	v, e := a.system.Configuration(ctx, 0)
	if e != nil {
		t.Fatal(e)
	}
	days := map[string]int{"login-log-saved-limit-days": 1}
	expect(t, call(t, srv.URL, "POST", "/api/admin/audit-retention/preview", map[string]any{"revision": v.ID, "days": days}, admin, origin), 200)
	result, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": days}, admin)
	expect(t, result, 202)
	for i := 0; i < 30; i++ {
		var id string
		if e = p.QueryRow(ctx, `INSERT INTO login_logs(actor_id,outcome,request_id,source_ip,created_at) VALUES($1,'succeeded','race','',now()-interval '2 days') RETURNING id`, actor).Scan(&id); e != nil {
			t.Fatal(e)
		}
		start := make(chan struct{})
		var holdErr, cleanupErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			holdErr = a.db.Within(ctx, func(c context.Context) error {
				return a.system.SetAuditHold(c, "login", id, "race verification", actor, true)
			})
		}()
		go func() { defer wg.Done(); <-start; cleanupErr = a.CleanupAccessRecords(ctx) }()
		close(start)
		wg.Wait()
		if cleanupErr != nil {
			t.Fatal(cleanupErr)
		}
		if holdErr != nil && !errors.Is(holdErr, pgx.ErrNoRows) {
			t.Fatal(holdErr)
		}
		var exists, held bool
		if e = p.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM login_logs WHERE id=$1),EXISTS(SELECT 1 FROM audit_holds WHERE category='login' AND record_id=$1)`, id).Scan(&exists, &held); e != nil {
			t.Fatal(e)
		}
		if holdErr == nil && (!exists || !held) {
			t.Fatal("successful hold raced with deletion")
		}
		if holdErr != nil && (exists || held) {
			t.Fatal("purged record has hold")
		}
	}
}
