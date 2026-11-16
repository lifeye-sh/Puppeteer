package application

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"puppeteer/backend/internal/platform/jobs"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMP4ColdWakeFailurePreservesQueue(t *testing.T) {
	_, a, _ := r02Server(t, false)
	ctx := context.Background()
	a.config.MP4Directory = t.TempDir()
	a.config.MP4OnDemand = true
	a.config.DeployTokenFile = t.TempDir() + "/not-enrolled"
	// Empty queues must not require an executor.
	if _, err := a.claimRecordingExport(ctx); !jobs.IsEmpty(err) {
		t.Fatal(err)
	}
	id, err := jobs.New(a.db).Enqueue(ctx, "recording.mp4", "queued", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err = a.claimRecordingExport(ctx); err == nil || err.Error() != "renderer_executor_unavailable" {
			t.Fatal(err)
		}
	}
	var state, reason string
	var untouched bool
	if err = a.db.Row(ctx, `SELECT state,reason,lease_token IS NULL AND started_at IS NULL FROM jobs WHERE id=$1`, id).Scan(&state, &reason, &untouched); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || !untouched || reason != "renderer_executor_unavailable" {
		t.Fatal(state, reason, untouched)
	}
	dir := t.TempDir()
	a.config.DeployTokenFile = filepath.Join(dir, "token")
	a.config.DeploySocket = filepath.Join(dir, "d.sock")
	if err = os.WriteFile(a.config.DeployTokenFile, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", a.config.DeploySocket)
	if err != nil {
		t.Fatal(err)
	}
	var response atomic.Int32
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/renderer/wake" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Error("wrong executor request")
		}
		writeJSON(w, int(response.Load()), map[string]bool{"ready": response.Load() == 200})
	})}
	go server.Serve(listener)
	defer server.Close()
	for _, tc := range []struct {
		status int32
		reason string
	}{{409, "deployment_busy"}, {501, "executor_upgrade_required"}, {503, "renderer_start_failed"}} {
		response.Store(tc.status)
		if _, err = a.claimRecordingExport(ctx); err == nil || err.Error() != tc.reason {
			t.Fatal(err)
		}
		if err = a.db.Row(ctx, `SELECT state,lease_token IS NULL AND started_at IS NULL FROM jobs WHERE id=$1`, id).Scan(&state, &untouched); err != nil || state != "queued" || !untouched {
			t.Fatal("wake failure claimed job", state, err)
		}
	}
	response.Store(200)
	j, err := a.claimRecordingExport(ctx)
	if err != nil || j.ID != id {
		t.Fatal("ready executor did not release queue", j, err)
	}
}

func TestMP4PressurePreservesQueueAndRecovers(t *testing.T) {
	_, a, _ := r02Server(t, false)
	ctx := context.Background()
	id, err := jobs.New(a.db).Enqueue(ctx, "recording.mp4", "queued", "")
	if err != nil {
		t.Fatal(err)
	}
	check := func(reason string) {
		t.Helper()
		var state, got string
		var untouched bool
		if e := a.db.Row(ctx, `SELECT state,reason,lease_token IS NULL AND started_at IS NULL FROM jobs WHERE id=$1`, id).Scan(&state, &got, &untouched); e != nil || state != "queued" || got != reason || !untouched {
			t.Fatal(state, got, untouched, e)
		}
	}
	for _, reason := range []string{"export_disk_pressure", "export_memory_pressure", "resource_probe_unavailable"} {
		for range 3 {
			if _, err = a.claimRecordingExportWhen(ctx, func() error { return errors.New(reason) }); !jobs.IsEmpty(err) {
				t.Fatal(err)
			}
		}
		check(reason)
	}
	var status atomic.Int32
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runtime/admission" || r.Method != "GET" {
			t.Error("unexpected probe")
		}
		writeJSON(w, int(status.Load()), map[string]string{"error": "renderer_memory_pressure"})
	}))
	defer renderer.Close()
	a.config.MP4URL = renderer.URL
	for _, tc := range []struct {
		code   int32
		reason string
	}{{503, "renderer_memory_pressure"}, {507, "temporary_storage_full"}, {429, "renderer_busy"}} {
		status.Store(tc.code)
		if _, err = a.claimRecordingExportWhen(ctx, func() error { return nil }); !jobs.IsEmpty(err) {
			t.Fatal(err)
		}
		check(tc.reason)
	}
	status.Store(200)
	job, err := a.claimRecordingExportWhen(ctx, func() error { return nil })
	if err != nil || job.ID != id {
		t.Fatal(job, err)
	}
	var reason string
	if err = a.db.Row(ctx, `SELECT reason FROM jobs WHERE id=$1`, id).Scan(&reason); err != nil || reason != "" {
		t.Fatal("stale pressure reason", reason, err)
	}
	// Empty/cancelled queues never probe external dependencies.
	if _, err = a.claimRecordingExportWhen(ctx, func() error { t.Fatal("empty queue probed"); return nil }); !jobs.IsEmpty(err) {
		t.Fatal(err)
	}
}
