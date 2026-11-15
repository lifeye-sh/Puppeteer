package application

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/net/webdav"
	"puppeteer/backend/internal/platform/configstate"
)

func TestBackupDependencyVerificationCleanupAndRecovery(t *testing.T) {
	_, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	a.config.BackupDirectory = t.TempDir()
	remoteDir := t.TempDir()
	var denyDelete atomic.Bool
	var writes atomic.Int32
	handler := &webdav.Handler{FileSystem: webdav.Dir(remoteDir), LockSystem: webdav.NewMemLS()}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "fixture" || p != "artifact-secret" {
			w.WriteHeader(401)
			return
		}
		if r.Method == "DELETE" && denyDelete.Load() {
			w.WriteHeader(403)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "PROPFIND" {
			writes.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	defer remote.Close()
	id := postID(t, srv, "/api/admin/backup/destinations", map[string]any{"name": "probe target", "kind": "webdav", "endpoint": remote.URL, "username": "fixture", "secret": "artifact-secret", "enabled": false, "allowHttp": true, "password": testPassword}, admin)
	feature := "backup-destination:" + id
	start := func(mode string) configstate.Task {
		t.Helper()
		snapshot := object(t, call(t, srv.URL, "GET", "/api/settings/operations/"+feature, nil, admin, origin))
		_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
		r, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/"+feature+"/"+mode, tag, map[string]any{"evidence": snapshot["evidence"]}, admin)
		expect(t, r, 202)
		return configurationTaskResult(t, srv, admin, object(t, r)["id"].(string))
	}
	if task := start("preflight"); task.State != "succeeded" || writes.Load() != 0 {
		t.Fatal("readonly probe mutated destination", task, writes.Load())
	}
	if task := start("verify"); task.State != "succeeded" || task.Mode != "verify" {
		t.Fatal(task)
	}
	entries, e := os.ReadDir(remoteDir)
	if e != nil || len(entries) != 0 {
		t.Fatal("successful verification left objects", entries, e)
	}
	denyDelete.Store(true)
	failed := start("verify")
	if failed.State != "failed" {
		t.Fatal("cleanup failure reported success", failed)
	}
	var pending int
	var sealed []byte
	if e = a.db.Row(ctx, `SELECT count(*) FROM dependency_probe_artifacts WHERE resource_id=$1`, id).Scan(&pending); e != nil || pending != 1 {
		t.Fatal("missing cleanup record", pending, e)
	}
	if e = a.db.Row(ctx, `SELECT sealed FROM dependency_probe_artifacts WHERE task_id=$1`, failed.ID).Scan(&sealed); e != nil || strings.Contains(string(sealed), "artifact-secret") {
		t.Fatal("unencrypted cleanup credentials", e)
	}
	// Simulate a worker restart: only persisted artifacts are used for recovery.
	denyDelete.Store(false)
	a.RecoverDependencyArtifacts(ctx)
	if e = a.db.Row(ctx, `SELECT count(*) FROM dependency_probe_artifacts WHERE resource_id=$1`, id).Scan(&pending); e != nil || pending != 0 {
		t.Fatal("cleanup recovery failed", pending, e)
	}
	entries, e = os.ReadDir(remoteDir)
	if e != nil || len(entries) != 0 {
		t.Fatal("recovery left objects", entries, e)
	}
	if task := start("verify"); task.State != "succeeded" {
		t.Fatal("retry failed", task)
	}
	history := call(t, srv.URL, "GET", "/api/settings/operation-tasks", nil, admin, origin)
	expect(t, history, 200)
	if strings.Contains(string(history.body), "artifact-secret") || strings.Contains(string(history.body), remote.URL) {
		t.Fatal("verification leaked destination credentials")
	}
}
