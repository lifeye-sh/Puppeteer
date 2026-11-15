package application

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"puppeteer/backend/internal/modules/system"
	"puppeteer/backend/internal/platform/configstate"
	"puppeteer/backend/internal/platform/listener"
)

func configurationHostFixture(t *testing.T, a *App, generation *atomic.Uint64, probe func(http.ResponseWriter, *http.Request), components ...map[string]any) {
	t.Helper()
	dir := t.TempDir()
	socket := filepath.Join(dir, "check.sock")
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("0123456789abcdef0123456789abcdef"), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/v1/status" {
			writeJSON(w, 200, map[string]any{"version": 1, "generation": generation.Load(), "mappings": map[string]any{"ssh": map[string]any{"bind": "127.0.0.1", "published": 2022}}, "history": []any{}, "task": nil, "components": func() map[string]any {
				if len(components) > 0 {
					return components[0]
				}
				return nil
			}()})
			return
		}
		if r.Method == "POST" && r.URL.Path == "/v1/preflight" {
			probe(w, r)
			return
		}
		t.Errorf("unexpected side effect %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	a.config.DeploySocket = socket
	a.config.DeployTokenFile = tokenFile
	a.config.ManagedDeploymentProtocols = map[string]bool{"ssh": true}
}

func configurationTaskResult(t *testing.T, srv *httptest.Server, cookie *http.Cookie, id string) configstate.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r := call(t, srv.URL, "GET", "/api/settings/operation-tasks", nil, cookie, origin)
		expect(t, r, 200)
		var body struct {
			Items []configstate.Task `json:"items"`
		}
		if err := json.Unmarshal(r.body, &body); err != nil {
			t.Fatal(err)
		}
		for _, task := range body.Items {
			if task.ID == id && task.State != "running" {
				return task
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("check did not finish")
	return configstate.Task{}
}

func TestConfigurationOperationsAuthorizationAndUnknownDependencies(t *testing.T) {
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("check-member", nil, nil), admin)
	member := loginAs(t, srv, "check-member")
	for _, path := range []string{"/api/settings/operations", "/api/settings/operation-tasks"} {
		expect(t, call(t, srv.URL, "GET", path, nil, member, origin), 403)
	}
	expect(t, call(t, srv.URL, "POST", "/api/settings/operations/ssh/preflight", map[string]any{}, member, origin), 403)
	r := call(t, srv.URL, "GET", "/api/settings/operations", nil, admin, origin)
	expect(t, r, 200)
	var body struct {
		Items []configurationFeature `json:"items"`
	}
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatal(err)
	}
	for _, item := range body.Items {
		if item.Category == "policies" && (item.Dependencies.State != "not_applicable" || item.Service.State != "not_applicable") {
			t.Fatal(item)
		}
		if item.ID == "mail" && item.Dependencies.State != "unknown" {
			t.Fatal(item)
		}
		if item.ID == "ssh" && item.Service.State != "unknown" {
			t.Fatal("flag is not runtime evidence")
		}
	}
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	unsupported, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/login/preflight", tag, map[string]any{}, admin)
	expect(t, unsupported, 422)
	stale, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/ssh/preflight", `"settings-999999"`, map[string]any{}, admin)
	expect(t, stale, 409)
	spoof, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/ssh/preflight", tag, map[string]any{"actor": "fake"}, admin)
	expect(t, spoof, 400)
}

func TestConfigurationOperationsPersistenceFreshnessAndRedaction(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	var gen atomic.Uint64
	gen.Store(4)
	configurationHostFixture(t, a, &gen, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"passed": true}) })
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	r, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/ssh/preflight", tag, map[string]any{}, admin)
	expect(t, r, 202)
	id := object(t, r)["id"].(string)
	if got := configurationTaskResult(t, srv, admin, id); got.State != "succeeded" {
		t.Fatal(got)
	}
	dependency := func(s *httptest.Server) string {
		resp := call(t, s.URL, "GET", "/api/settings/operations", nil, admin, origin)
		expect(t, resp, 200)
		var body struct {
			Items []configurationFeature `json:"items"`
		}
		if err := json.Unmarshal(resp.body, &body); err != nil {
			t.Fatal(err)
		}
		for _, item := range body.Items {
			if item.ID == "ssh" {
				return item.Dependencies.State
			}
		}
		t.Fatal("missing ssh")
		return ""
	}
	if got := dependency(srv); got != "ready" {
		t.Fatal(got)
	}
	gen.Store(5)
	if got := dependency(srv); got != "stale" {
		t.Fatal("host generation", got)
	}
	gen.Store(4)
	// New process identity must not inherit another node's healthy evidence.
	restarted := New(pool, a.config)
	other := httptest.NewServer(restarted.Handler())
	defer other.Close()
	if got := dependency(other); got != "stale" {
		t.Fatal("restart", got)
	}
	if _, err := a.db.Exec(context.Background(), `UPDATE configuration_checks SET payload=jsonb_set(payload,'{expires}',to_jsonb($2::text)) WHERE id=$1`, id, time.Now().Add(-time.Second).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if got := dependency(srv); got != "stale" {
		t.Fatal("expiry", got)
	}
}

func TestConfigurationOperationsDeduplicateCancelAndSanitize(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	var gen atomic.Uint64
	gen.Store(1)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	configurationHostFixture(t, a, &gen, func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		writeJSON(w, 422, map[string]any{"error": "port conflict https://admin:SUPER_SECRET@example.test?token=PRIVATE"})
	})
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	r, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/ssh/preflight", tag, map[string]any{}, admin)
	expect(t, r, 202)
	id := object(t, r)["id"].(string)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("host not reached")
	}
	duplicate, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/ssh/preflight", tag, map[string]any{}, admin)
	expect(t, duplicate, 202)
	if object(t, duplicate)["id"] != id {
		t.Fatal("not idempotent")
	}
	conflict, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/mysql/preflight", tag, map[string]any{}, admin)
	expect(t, conflict, 409)
	expect(t, call(t, srv.URL, "POST", "/api/settings/operation-tasks/"+id+"/cancel", map[string]any{}, admin, origin), 204)
	if result := configurationTaskResult(t, srv, admin, id); result.State != "cancelled" {
		t.Fatal(result)
	}
	// Cancellation is persistent, late worker results cannot overwrite it.
	if a.storeConfigurationCheck(context.Background(), configstate.Task{ID: id, State: "succeeded"}) {
		t.Fatal("late worker overwrote cancellation")
	}
}

func TestConfigurationOperationsFailureNeverReturnsRawProbeError(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	var gen atomic.Uint64
	configurationHostFixture(t, a, &gen, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 422, map[string]any{"passed": false, "error": "port conflict https://admin:SUPER_SECRET@example.test?token=PRIVATE"})
	})
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	r, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/ssh/preflight", tag, map[string]any{}, admin)
	expect(t, r, 202)
	result := configurationTaskResult(t, srv, admin, object(t, r)["id"].(string))
	raw, _ := json.Marshal(result)
	if result.State != "failed" || !strings.Contains(string(raw), "port_check_failed") {
		t.Fatal(string(raw))
	}
	if strings.Contains(string(raw), "SUPER_SECRET") || strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("secret leaked")
	}
}

func TestConfigurationOperationsListenerAndNodeEvidence(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := listener.New(ctx, 2)
	defer m.Close()
	p, err := m.Prepare(ctx, []listener.Spec{{Name: "ssh", Address: "127.0.0.1:0", Handle: func(_ context.Context, c net.Conn) { c.Close() }}})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Commit(); err != nil {
		t.Fatal(err)
	}
	a.listenerManager = m
	if _, err = a.system.ApplyConfiguration(ctx, a.configurationNode, func(system.Revision) error { return nil }); err != nil {
		t.Fatal(err)
	}
	service := func() string {
		r := call(t, srv.URL, "GET", "/api/settings/operations", nil, admin, origin)
		expect(t, r, 200)
		var body struct {
			Items []configurationFeature `json:"items"`
		}
		if err := json.Unmarshal(r.body, &body); err != nil {
			t.Fatal(err)
		}
		for _, item := range body.Items {
			if item.ID == "ssh" {
				return item.Service.State
			}
		}
		return "missing"
	}
	if state := service(); state != "running" {
		t.Fatal(state)
	}
	if _, err = a.db.Exec(ctx, `UPDATE configuration_nodes SET last_seen=now()-interval '1 minute' WHERE id=$1`, a.configurationNode); err != nil {
		t.Fatal(err)
	}
	if state := service(); state != "degraded" {
		t.Fatal("offline node must not report healthy service", state)
	}
}

func TestConfigurationOperationsExpiredTaskAndRestoreInvalidation(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ctx := context.Background()
	revision, err := a.system.Configuration(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := configstate.Task{ID: "interrupted-test", Feature: "ssh", Node: a.configurationNode, Revision: revision.ID, Actor: "test", State: "running", Stage: "host", Started: now.Add(-time.Minute), Updated: now, Deadline: now.Add(-time.Second), Expires: now.Add(time.Minute), Checks: []configstate.Check{}}
	raw, _ := json.Marshal(task)
	if _, err = a.db.Exec(ctx, `INSERT INTO configuration_checks(id,feature,node_id,revision,actor_id,state,deadline,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, task.ID, task.Feature, task.Node, task.Revision, task.Actor, task.State, task.Deadline, raw); err != nil {
		t.Fatal(err)
	}
	if got := configurationTaskResult(t, srv, admin, task.ID); got.State != "interrupted" {
		t.Fatal(got)
	}
	// A timed-out journal entry must not deadlock subsequent admissions.
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	r, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/ssh/preflight", tag, map[string]any{}, admin)
	expect(t, r, 202)
	configurationTaskResult(t, srv, admin, object(t, r)["id"].(string))
	if err = a.system.QuarantineRestore(ctx); err != nil {
		t.Fatal(err)
	}
	var active int
	if err = a.db.Row(ctx, `SELECT count(*) FROM configuration_checks WHERE state<>'stale' OR payload->>'state'<>'stale'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatal("restore inherited preflight readiness")
	}
}

func TestConfigurationOperationsRechecksHostGenerationAfterProbe(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	var gen atomic.Uint64
	configurationHostFixture(t, a, &gen, func(w http.ResponseWriter, r *http.Request) {
		gen.Add(1)
		writeJSON(w, 200, map[string]any{"passed": true})
	})
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	r, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/ssh/preflight", tag, map[string]any{}, admin)
	expect(t, r, 202)
	result := configurationTaskResult(t, srv, admin, object(t, r)["id"].(string))
	if result.State != "failed" || result.Checks[len(result.Checks)-1].Code != "deployment_changed" {
		t.Fatal(result)
	}
}

func TestConfigurationGraphicsCapabilityAndObservedStop(t *testing.T) {
	for _, feature := range []string{"graphics", "rdp-drive", "mp4"} {
		for _, supported := range []bool{false, true} {
			t.Run(feature+map[bool]string{false: "old-executor", true: "enrolled"}[supported], func(t *testing.T) {
				_, a, srv := r02Server(t, false)
				admin := loginAs(t, srv, "accept-admin")
				var gen atomic.Uint64
				components := map[string]any{}
				if supported {
					components[feature] = map[string]any{"supported": true, "enabled": false, "state": "stopped", "reason": "container stopped"}
				}
				configurationHostFixture(t, a, &gen, func(w http.ResponseWriter, r *http.Request) {
					var input struct {
						Protocol, Bind string
						Port           int
						Enabled        bool
					}
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						t.Error(err)
					}
					if input.Protocol != feature || input.Bind != "" || input.Port != 0 || input.Enabled {
						t.Errorf("unexpected port operation: %+v", input)
					}
					writeJSON(w, 200, map[string]any{"passed": true})
				}, components)
				response := call(t, srv.URL, "GET", "/api/settings/operations", nil, admin, origin)
				expect(t, response, 200)
				var body struct {
					Items []configurationFeature `json:"items"`
				}
				json.Unmarshal(response.body, &body)
				found := false
				for _, item := range body.Items {
					if item.ID == feature {
						found = true
						if item.CheckSupported != supported {
							t.Fatal(item)
						}
						if supported && item.Service.State != "stopped" {
							t.Fatal("actual stopped evidence missing", item)
						}
						if !supported && (item.Dependencies.State != "blocked" || item.Service.State != "unknown") {
							t.Fatal("old executor assumed ready", item)
						}
					}
				}
				if !found {
					t.Fatal("missing graphics feature")
				}
				_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
				result, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/"+feature+"/preflight", tag, map[string]any{}, admin)
				expect(t, result, 202)
				task := configurationTaskResult(t, srv, admin, object(t, result)["id"].(string))
				if supported && task.State != "succeeded" || !supported && task.State != "failed" {
					t.Fatal(task)
				}
			})
		}
	}
}
