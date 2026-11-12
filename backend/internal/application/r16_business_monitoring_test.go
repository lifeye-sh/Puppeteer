package application

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/modules/database"
	"next-terminal/backend/internal/modules/notification"
	"next-terminal/backend/internal/modules/operations"
	"next-terminal/backend/internal/modules/pki"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/modules/storage"
	"sync"
	"testing"
)

func TestR16BusinessMonitoringSamplesAndTransitions(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	actor, e := a.identity.FindByName(ctx, "accept-admin")
	if e != nil {
		t.Fatal(e)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	var catalog []operations.MonitorMetric
	response := call(t, srv.URL, "GET", "/api/admin/monitor-metrics", nil, admin, origin)
	expect(t, response, 200)
	if e = json.Unmarshal(response.body, &catalog); e != nil {
		t.Fatal(e)
	}
	if len(catalog) != 13 {
		t.Fatal("metric catalog incomplete", len(catalog))
	}
	memberID := postID(t, srv, "/api/admin/users", userInput("monitor-business-member", nil, nil), admin)
	member := loginAs(t, srv, "monitor-business-member")
	expect(t, call(t, srv.URL, "GET", "/api/admin/monitor-metrics", nil, member, origin), 403)
	for _, metric := range catalog {
		if metric.Scope != "platform" || metric.Key == "configuration.delay" {
			continue
		}
		threshold, recovery := 1.0, 0.0
		if metric.Key == "storage.usage" {
			threshold, recovery = 80, 60
		}
		if metric.Unit == "秒" {
			threshold = 60
		}
		r := operations.MonitorRule{Name: metric.Key, Metric: metric.Key, Threshold: threshold, Recovery: recovery, Enabled: true}
		postID(t, srv, "/api/admin/monitor-rules", r, admin)
		r.AgentID = actor.ID
		expect(t, call(t, srv.URL, "POST", "/api/admin/monitor-rules", r, admin, origin), 422)
		r.AgentID = ""
		r.Threshold = metric.Max + 1
		expect(t, call(t, srv.URL, "POST", "/api/admin/monitor-rules", r, admin, origin), 422)
	}
	evaluate := func() {
		t.Helper()
		if e := a.evaluateMonitors(ctx); e != nil {
			t.Fatal(e)
		}
	}
	evaluate()
	exec(`INSERT INTO jobs(kind,state,created_at) VALUES('fixture','queued',now()-interval '120 seconds'),('fixture','running',now()-interval '1 day')`)
	exec(`INSERT INTO jobs(kind,state,cancel_requested,created_at) VALUES('fixture','queued',true,now()-interval '1 day')`)
	exec(`INSERT INTO jobs(kind,state,ended_at) VALUES('fixture','failed',now()),('fixture','partial',now()),('backup.create','unknown',now()),('fixture','cancelled',now()),('fixture','failed',now()-interval '16 minutes')`)
	var channel, delivery string
	if e = pool.QueryRow(ctx, `INSERT INTO notification_channels(name,type,enabled,config) VALUES('metrics fixture','smtp',false,'{}') RETURNING id`).Scan(&channel); e != nil {
		t.Fatal(e)
	}
	if e = pool.QueryRow(ctx, `INSERT INTO notification_deliveries(channel_id,recipient_id,status,attempts,next_attempt) VALUES($1,$2,'pending',4,now()-interval '1 minute') RETURNING id`, channel, actor.ID).Scan(&delivery); e != nil {
		t.Fatal(e)
	}
	// Real worker claims the fifth attempt and records its timestamp; missing
	// eligibility fails without dispatching any external notification.
	if e = notification.New(a.db, a.config.VaultKey).DeliverOne(ctx, nil); e != nil {
		t.Fatal(e)
	}
	var terminal bool
	if e = pool.QueryRow(ctx, `SELECT status='failed' AND last_attempt_at IS NOT NULL FROM notification_deliveries WHERE id=$1`, delivery).Scan(&terminal); e != nil || !terminal {
		t.Fatal("attempt timestamp not persisted", e)
	}
	exec(`INSERT INTO notification_deliveries(channel_id,recipient_id,status,next_attempt) VALUES($1,$2,'pending',now()-interval '120 seconds'),($1,$2,'pending',now()+interval '1 hour'),($1,$2,'failed',now())`, channel, actor.ID)
	store := storage.New(a.db)
	root, e := store.Ensure(ctx, memberID)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.SetQuota(ctx, memberID, 100); e != nil {
		t.Fatal(e)
	}
	file, e := store.Create(ctx, memberID, root.ID, "monitor-usage", false)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.Write(ctx, memberID, file.ID, 0, make([]byte, 90)); e != nil {
		t.Fatal(e)
	}
	host, port, _ := sshTarget(t)
	assetID := r04Asset(t, srv, admin, host, port, true)
	hosts, _, e := a.resources.List(ctx, resource.Filter{ID: assetID, Limit: 1, All: true})
	if e != nil || len(hosts) != 1 {
		t.Fatal(e)
	}
	asset := hosts[0]
	sessionID, e := session.New(a.db).Reserve(ctx, session.Session{ActorID: actor.ID, AssetID: assetID, Protocol: "ssh", AssetVersion: 1, CredentialVersion: 1}, "monitor-ticket", "monitor-auth")
	if e != nil {
		t.Fatal(e)
	}
	exec(`UPDATE sessions SET lease_expires_at=now()+interval '5 minutes' WHERE id=$1`, sessionID)
	exec(`INSERT INTO recordings(session_id,state,ended_at) VALUES($1,'interrupted',now())`, sessionID)
	ca, key, e := pki.Generate("metrics DB", []string{"db.internal"}, 2, true, nil)
	clear(key)
	if e != nil {
		t.Fatal(e)
	}
	dbService := database.New(a.db)
	dbAsset := database.Asset{Name: "metrics DB", Alias: "metrics_db", Host: "db.internal", Port: 3306, Database: "inventory", ReadonlyCredentialID: asset.CredentialID, TLSServerName: "db.internal", TLSCA: string(ca), Enabled: true, ConnectTimeout: 10, Engine: "mysql"}
	dbAsset.ID, e = dbService.Save(ctx, dbAsset)
	if e != nil {
		t.Fatal(e)
	}
	exec(`INSERT INTO database_sessions(user_id,asset_id,password_version,asset_version,remote_address,quota_epoch,quota_lease_until) SELECT $1,$2,1,1,'fixture',auth_epoch,now()+interval '5 minutes' FROM instance_state WHERE singleton=1`, actor.ID, dbAsset.ID)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := a.evaluateMonitors(ctx); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	rules, e := a.operations().MonitorRules(ctx)
	if e != nil {
		t.Fatal(e)
	}
	expected := map[string]float64{"queue.failed": 3, "notification.failed": 1, "storage.usage": 90, "sessions.active": 2, "recording.incomplete": 1, "backup.failed": 1}
	for _, r := range rules {
		if !r.Firing || r.Value == nil {
			t.Fatal("rule failed to fire", r)
		}
		if want, ok := expected[r.Metric]; ok && *r.Value != want {
			t.Fatal(r.Metric, *r.Value, want)
		}
		if r.Metric == "queue.age" || r.Metric == "notification.delay" {
			if *r.Value < 120 || *r.Value > 180 {
				t.Fatal("wrong age", r.Metric, *r.Value)
			}
		}
	}
	var events, notifications int
	if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM system_events WHERE kind='firing'),(SELECT count(*) FROM notification_events WHERE event_type='monitor.firing')`).Scan(&events, &notifications); e != nil || events != 8 || notifications != 8 {
		t.Fatal("duplicate/missing transitions", events, notifications, e)
	}
	// Reduce quota below existing bytes: over-100% is a real sample, not unknown.
	if e = store.SetQuota(ctx, memberID, 50); e != nil {
		t.Fatal(e)
	}
	evaluate()
	var usage float64
	if e = pool.QueryRow(ctx, `SELECT last_value FROM monitor_rules WHERE metric='storage.usage'`).Scan(&usage); e != nil || usage != 180 {
		t.Fatal("over quota hidden", usage, e)
	}
	// Recover naturally by draining queues, ending leases, freeing bytes, and
	// moving terminal events outside the explicitly documented 15-minute window.
	exec(`UPDATE jobs SET state='succeeded',ended_at=now()`)
	exec(`UPDATE notification_deliveries SET status='delivered'; UPDATE sessions SET lease_expires_at=now()-interval '1 second'; UPDATE database_sessions SET quota_lease_until=now()-interval '1 second'; UPDATE recordings SET ended_at=now()-interval '16 minutes'`)
	if e = store.Resize(ctx, memberID, file.ID, 0); e != nil {
		t.Fatal(e)
	}
	evaluate()
	evaluate()
	if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM system_events WHERE kind='recovered'),(SELECT count(*) FROM notification_events WHERE event_type='monitor.recovered')`).Scan(&events, &notifications); e != nil || events != 8 || notifications != 8 {
		t.Fatal("recovery not idempotent", events, notifications, e)
	}
}
