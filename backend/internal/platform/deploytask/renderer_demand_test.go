package deploytask

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMP4DemandPlanPreservesModeWhenDisabled(t *testing.T) {
	p, err := planMP4(mp4Fixture(), Request{Protocol: "mp4", Enabled: true, RendererMode: "on-demand", RendererQuality: "economy"})
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		p, err = planMP4(p.Document, Request{Protocol: "mp4", Enabled: enabled})
		if err != nil {
			t.Fatal(err)
		}
		mode, quality := rendererSettings(p.Document)
		var m composeModel
		_ = json.Unmarshal(p.Document, &m)
		if mode != "on-demand" || quality != "economy" || string(m.Services["mp4-renderer"]["restart"]) != `"no"` {
			t.Fatal(string(p.Document))
		}
	}
	for _, r := range []Request{{Protocol: "ssh", RendererMode: "on-demand"}, {Protocol: "mp4", RendererMode: "other"}, {Protocol: "mp4", RendererQuality: "other"}} {
		if Validate(r) == nil {
			t.Fatal("invalid options accepted", r)
		}
	}
}

func TestMP4WakeAndSleepOnlyTouchRenderer(t *testing.T) {
	e := fixtureExecutor(t, `printf '%s\n' "$*" >> calls
case "$*" in
 *"image inspect"*) echo v1;;
 *"ps --format"*) echo '[{"Service":"mp4-renderer","State":"running","Health":"healthy"}]';;
 *"exec -T mp4-renderer"*) echo '{"ready":true,"drained":true}';;
esac
`)
	p, err := planMP4(mp4Fixture(), Request{Protocol: "mp4", Enabled: true, RendererMode: "on-demand"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(e.Files[0], p.Document, 0600); err != nil {
		t.Fatal(err)
	}
	d := &HostDriver{Executor: e}
	ctx := context.Background()
	if err = d.RendererWake(ctx); err != nil {
		t.Fatal(err)
	}
	if err = d.idleRenderer(ctx); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(filepath.Join(e.Directory, "calls"))
	if strings.Contains(string(calls), "stop --timeout") {
		t.Fatal("lease did not protect job preparation")
	}
	d.rendererLeaseUntil.Store(time.Now().Add(-time.Second).UnixNano())
	if err = d.idleRenderer(ctx); err != nil {
		t.Fatal(err)
	}
	calls, _ = os.ReadFile(filepath.Join(e.Directory, "calls"))
	for _, s := range []string{"up -d --no-deps --no-build --pull never --no-recreate --wait --wait-timeout 25 mp4-renderer", "runtime/drain", "stop --timeout 15 mp4-renderer"} {
		if !strings.Contains(string(calls), s) {
			t.Fatal(string(calls))
		}
	}
	for _, s := range []string{"120 server", "120 web", "force-recreate", " pull ", "stop --timeout 30 server"} {
		if strings.Contains(string(calls), s) {
			t.Fatal("routine wake mutated application", string(calls))
		}
	}
	p, err = planMP4(p.Document, Request{Protocol: "mp4", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(e.Files[0], p.Document, 0600)
	if d.RendererWake(ctx) == nil {
		t.Fatal("disabled renderer woke")
	}
}

type runtimeDriver struct {
	fakeDriver
	calls int
}

func (d *runtimeDriver) RendererWake(context.Context) error  { d.calls++; return nil }
func (d *runtimeDriver) RendererTouch(context.Context) error { d.calls++; return nil }
func TestMP4RuntimeAuthenticationAndDeploymentGate(t *testing.T) {
	d := &runtimeDriver{}
	e, err := Open(context.Background(), t.TempDir(), d)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	h, err := Handler(context.Background(), e, token)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"wake", "touch"} {
		for _, auth := range []bool{false, true} {
			r := httptest.NewRequest("POST", "/v1/renderer/"+operation, nil)
			if auth {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 401
			if auth {
				want = 200
			}
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
	if d.calls != 2 {
		t.Fatal(d.calls)
	}
	if err = e.Maintain(context.Background(), func(context.Context) error {
		r := httptest.NewRequest("POST", "/v1/renderer/wake", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 409 || d.calls != 2 {
			t.Fatal("wake bypassed deployment gate", w.Code)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMP4StandbyDoesNotHideAbnormalExit(t *testing.T) {
	plan, err := planMP4(mp4Fixture(), Request{Protocol: "mp4", Enabled: true, RendererMode: "on-demand"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ code, state string }{{"0", "standby"}, {"137", "failed"}} {
		e := fixtureExecutor(t, `echo '[{"Service":"mp4-renderer","State":"exited","ExitCode":`+tc.code+`}]'`)
		d := &HostDriver{Executor: e}
		result := d.withMP4Component(context.Background(), plan.Document, map[string]ComponentStatus{})
		if result["mp4"].State != tc.state {
			t.Fatal(result)
		}
	}
	var doc map[string]any
	_ = json.Unmarshal(plan.Document, &doc)
	doc["services"].(map[string]any)["mp4-renderer"].(map[string]any)["restart"] = "always"
	raw, _ := json.Marshal(doc)
	if _, err = planMP4(raw, Request{Protocol: "mp4", Enabled: true}); err == nil {
		t.Fatal("auto-restarting demand renderer accepted")
	}
}
