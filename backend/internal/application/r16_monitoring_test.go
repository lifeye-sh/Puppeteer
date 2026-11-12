package application

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/notification"
	"next-terminal/backend/internal/modules/operations"
	"sync"
	"sync/atomic"
	"testing"
)

func TestR16MonitoringTransitionsAndDelivery(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("monitor-member", nil, nil), admin)
	member := loginAs(t, srv, "monitor-member")
	r := call(t, srv.URL, "POST", "/api/admin/agent-registration-tokens", map[string]any{"name": "monitor-fixture", "networkScope": map[string]any{"cidrs": []string{"192.0.2.0/24"}, "ports": []int{22}}}, admin, origin)
	expect(t, r, 201)
	var device connectivity.Enrolled
	if err := a.db.Within(ctx, func(c context.Context) error {
		var e error
		device, e = a.connectivity.Enroll(c, connectivity.Enrollment{RegistrationToken: object(t, r)["token"].(string), Name: "monitor-fixture", OS: "linux", Arch: "amd64", Version: "test"})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	rule := operations.MonitorRule{Name: "CPU fixture", Metric: "agent.cpu", AgentID: device.AgentID, Threshold: 80, Recovery: 60, HoldSeconds: 30, Enabled: true}
	id := postID(t, srv, "/api/admin/monitor-rules", rule, admin)
	expect(t, call(t, srv.URL, "GET", "/api/admin/monitor-rules", nil, member, origin), 403)
	expect(t, call(t, srv.URL, "GET", "/api/admin/system-events", nil, member, origin), 403)
	bad := rule
	bad.Recovery = 90
	expect(t, call(t, srv.URL, "POST", "/api/admin/monitor-rules", bad, admin, origin), 422)
	var actor string
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE username='accept-admin'`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	var sent atomic.Int32
	vendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/cgi-bin/gettoken" {
			_, _ = w.Write([]byte(`{"access_token":"fixture-token"}`))
			return
		}
		sent.Add(1)
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	defer vendor.Close()
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: vendor.TLS.Certificates[0].Certificate[0]}))
	channel := postID(t, srv, "/api/admin/notifications/channels", notification.Channel{Name: "monitor internal fixture", Type: "wecom", Enabled: true, Secret: "fixture-secret", Config: notification.Config{APIBase: vendor.URL, CorpID: "fixture", AgentID: 1, CA: ca}}, admin)
	postID(t, srv, "/api/admin/notifications/bindings", notification.Binding{UserID: actor, ChannelID: channel, Address: "admin", Enabled: true}, admin)
	notify := notification.Rule{Name: "monitor notices", Enabled: true, Events: []string{"monitor.firing", "monitor.recovered"}, ChannelIDs: []string{channel}, Mode: "specified", RecipientIDs: []string{actor}}
	postID(t, srv, "/api/admin/notifications/rules", notify, admin)
	notify.Mode = "requester"
	expect(t, call(t, srv.URL, "POST", "/api/admin/notifications/rules", notify, admin, origin), 422)
	sample := func(cpu float64, stale bool) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"cpu": cpu, "intervalSeconds": 5, "memoryUsed": 90, "memoryTotal": 100, "disks": []any{map[string]any{"path": "/", "used": 90, "total": 100}}})
		_, err := pool.Exec(ctx, `UPDATE agent_gateway_configs SET metrics=$2,metrics_at=CASE WHEN $3 THEN now()-interval '1 minute' ELSE now() END WHERE id=$1`, device.AgentID, raw, stale)
		if err != nil {
			t.Fatal(err)
		}
	}
	evaluate := func() {
		t.Helper()
		if err := a.evaluateMonitors(ctx); err != nil {
			t.Fatal(err)
		}
	}
	count := func(want int) {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM system_events`).Scan(&n); err != nil || n != want {
			t.Fatal("event count", n, want, err)
		}
	}
	sample(90, false)
	evaluate()
	count(0)
	if _, err := pool.Exec(ctx, `UPDATE monitor_rules SET pending_at=now()-interval '31 seconds' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.evaluateMonitors(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	count(1)
	sample(0, true)
	evaluate()
	count(1)
	var active bool
	var state string
	if err := pool.QueryRow(ctx, `SELECT firing,sample_state FROM monitor_rules WHERE id=$1`, id).Scan(&active, &state); err != nil || !active || state != "unknown" {
		t.Fatal("stale data falsely recovered", active, state, err)
	}
	sample(70, false)
	evaluate()
	count(1) // hysteresis: still above recovery threshold
	sample(50, false)
	evaluate()
	evaluate()
	count(2)
	for i := 0; i < 2; i++ {
		if err := a.notifications().Expand(ctx); err != nil {
			t.Fatal(err)
		}
		if err := a.notifications().DeliverOne(ctx, a.notificationEligible); err != nil {
			t.Fatal(err)
		}
	}
	if sent.Load() != 2 {
		t.Fatal("alarm/recovery not delivered", sent.Load())
	}
	events := object(t, call(t, srv.URL, "GET", "/api/admin/system-events", nil, admin, origin))["items"].([]any)
	eventID := events[0].(map[string]any)["id"].(string)
	expect(t, call(t, srv.URL, "POST", "/api/admin/system-events/"+eventID+"/acknowledge", nil, member, origin), 403)
	expect(t, call(t, srv.URL, "POST", "/api/admin/system-events/"+eventID+"/acknowledge", nil, admin, origin), 204)
	expect(t, call(t, srv.URL, "POST", "/api/admin/system-events/"+eventID+"/acknowledge", nil, admin, origin), 204)
	// Editing is optimistic and does not silently overwrite another operator.
	rule.Version = 1
	rule.Enabled = false
	expect(t, call(t, srv.URL, "PUT", "/api/admin/monitor-rules/"+id, rule, admin, origin), 200)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/monitor-rules/"+id, rule, admin, origin), 409)
	// Remaining real metric sources, including missing telemetry and recovery.
	for _, metric := range []string{"agent.memory", "agent.disk"} {
		metricRule := operations.MonitorRule{Name: metric, Metric: metric, AgentID: device.AgentID, Threshold: 80, Recovery: 60, Enabled: true}
		metricID := postID(t, srv, "/api/admin/monitor-rules", metricRule, admin)
		evaluate()
		if err := pool.QueryRow(ctx, `SELECT firing FROM monitor_rules WHERE id=$1`, metricID).Scan(&active); err != nil || !active {
			t.Fatal(metric, "not firing", err)
		}
		expect(t, call(t, srv.URL, "DELETE", "/api/admin/monitor-rules/"+metricID+"?version=1", nil, admin, origin), 204)
		expect(t, call(t, srv.URL, "DELETE", "/api/admin/monitor-rules/"+metricID+"?version=1", nil, admin, origin), 409)
	}
	silence := postID(t, srv, "/api/admin/monitor-rules", operations.MonitorRule{Name: "silence", Metric: "agent.silence", AgentID: device.AgentID, Threshold: 20, Recovery: 10, Enabled: true}, admin)
	sample(50, true)
	evaluate()
	if err := pool.QueryRow(ctx, `SELECT firing FROM monitor_rules WHERE id=$1`, silence).Scan(&active); err != nil || !active {
		t.Fatal("silence not detected", err)
	}
	sample(50, false)
	evaluate()
	if err := pool.QueryRow(ctx, `SELECT firing FROM monitor_rules WHERE id=$1`, silence).Scan(&active); err != nil || active {
		t.Fatal("silence not recovered", err)
	}
	configuration := postID(t, srv, "/api/admin/monitor-rules", operations.MonitorRule{Name: "configuration", Metric: "configuration.delay", Threshold: 30, Recovery: 0, Enabled: true}, admin)
	if _, err := pool.Exec(ctx, `UPDATE configuration_revisions SET created_at=now()-interval '1 minute' WHERE id=(SELECT revision FROM configuration_head)`); err != nil {
		t.Fatal(err)
	}
	evaluate()
	if err := pool.QueryRow(ctx, `SELECT firing FROM monitor_rules WHERE id=$1`, configuration).Scan(&active); err != nil || !active {
		t.Fatal("configuration delay not detected", err)
	}
	if _, err := a.appliedConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	evaluate()
	if err := pool.QueryRow(ctx, `SELECT firing FROM monitor_rules WHERE id=$1`, configuration).Scan(&active); err != nil || active {
		t.Fatal("configuration ACK not recovered", err)
	}
	var previousCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM system_events`).Scan(&previousCount); err != nil {
		t.Fatal(err)
	}
	// Same-timestamp event bursts retain every row across the cursor boundary.
	if _, err := pool.Exec(ctx, `INSERT INTO system_events(rule_id,rule_name,metric,kind) SELECT $1,'cursor','agent.cpu','firing' FROM generate_series(1,105)`, id); err != nil {
		t.Fatal(err)
	}
	first := object(t, call(t, srv.URL, "GET", "/api/admin/system-events", nil, admin, origin))
	items := first["items"].([]any)
	last := items[len(items)-1].(map[string]any)
	params := url.Values{"before": {last["createdAt"].(string)}, "beforeId": {last["id"].(string)}}
	second := object(t, call(t, srv.URL, "GET", "/api/admin/system-events?"+params.Encode(), nil, admin, origin))
	if len(items) != 100 || len(second["items"].([]any)) != 5+previousCount || !first["hasMore"].(bool) {
		t.Fatal("pagination lost events", len(items), len(second["items"].([]any)))
	}
}
