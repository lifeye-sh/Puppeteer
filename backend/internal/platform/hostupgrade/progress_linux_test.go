//go:build linux

package hostupgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"puppeteer/backend/internal/platform/deploytask"
	"puppeteer/backend/internal/platform/systemupdate"
)

func progressFixture(t *testing.T, dir, stage string, started time.Time) systemupdate.UpgradeJournal {
	t.Helper()
	p := systemupdate.UpgradePlan{ID: strings.Repeat("a", 32), Actor: "private-actor", Source: "beta0.10", Target: "beta0.11", LayoutDigest: strings.Repeat("b", 64), Envelope: json.RawMessage(`{"private":"signed-envelope"}`)}
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	j := systemupdate.UpgradeJournal{Format: 1, Plan: p, PlanDigest: hex.EncodeToString(sum[:]), Stage: stage, Started: started, Updated: started, Events: []systemupdate.UpgradeEvent{{Stage: stage, At: started}}}
	if e := os.MkdirAll(filepath.Join(dir, p.ID), 0700); e != nil {
		t.Fatal(e)
	}
	raw, _ = json.Marshal(j)
	if e := os.WriteFile(filepath.Join(dir, "upgrade.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, p.ID, "progress-token"), []byte(strings.Repeat("c", 64)), 0600); e != nil {
		t.Fatal(e)
	}
	return j
}

func TestIndependentProgressTicketAndSanitization(t *testing.T) {
	dir := t.TempDir()
	j := progressFixture(t, dir, "recovery_required", time.Now())
	path := "/upgrade-progress/" + j.Plan.ID + "?ticket=" + strings.Repeat("c", 64)
	handler := progressHandler(dir)
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", path, 200}, {"POST", path, 401}, {"GET", "/upgrade-progress/other?ticket=" + strings.Repeat("c", 64), 401}, {"GET", "/upgrade-progress/" + j.Plan.ID, 401}, {"GET", path + "x", 401}} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.status == 200 {
			for _, secret := range []string{"private-actor", "signed-envelope", "layoutDigest", "progressToken", strings.Repeat("c", 64)} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatal("progress leaked protected field", secret)
				}
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("progress may be cached")
			}
		}
	}
	progressFixture(t, dir, "preparing", time.Now().Add(-5*time.Hour))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 401 {
		t.Fatal("expired ticket accepted")
	}
	if _, e := ServeProgress(dir, filepath.Join(dir, "upgrade.json"), 0); e == nil {
		t.Fatal("regular file replaced as socket")
	}
}

func TestControllerRejectsUnexpectedFieldsAndReservesPriorID(t *testing.T) {
	root := t.TempDir()
	driver := &deploytask.HostDriver{Executor: deploytask.ComposeExecutor{Directory: root}}
	engine, e := deploytask.Open(context.Background(), filepath.Join(root, "config", "deployd-state"), driver)
	if e != nil {
		t.Fatal(e)
	}
	c := &Controller{Engine: engine, Executor: driver.Executor}
	for _, body := range []string{`{"command":"id"}`, `{} {}`, `{"unknown":true}`} {
		w := httptest.NewRecorder()
		c.ServeHTTP(w, httptest.NewRequest("POST", "/v1/updates/tasks", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	j := progressFixture(t, c.directory(), "switching", time.Now())
	input := Request{ID: j.Plan.ID, Actor: j.Plan.Actor, Version: j.Plan.Target, LayoutDigest: j.Plan.LayoutDigest}
	raw, _ := json.Marshal(input)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("POST", "/v1/updates/tasks", strings.NewReader(string(raw))))
	if w.Code != 202 {
		t.Fatal("same task not idempotent", w.Code, w.Body.String())
	}
	input.Version = "beta0.12"
	raw, _ = json.Marshal(input)
	w = httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("POST", "/v1/updates/tasks", strings.NewReader(string(raw))))
	if w.Code != 409 {
		t.Fatal("ID reused", w.Code)
	}
	w = httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("POST", "/v1/updates/preflight", strings.NewReader(`{}`)))
	if w.Code != 409 {
		t.Fatal("active upgrade did not exclude preflight", w.Code, w.Body.String())
	}
}
