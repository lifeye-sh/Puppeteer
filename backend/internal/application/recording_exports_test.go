package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/platform/jobs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMP4RealRendererAfterClientDisconnect(t *testing.T) {
	target := os.Getenv("NT_MP4_TEST_URL")
	if target == "" {
		t.Skip("NT_MP4_TEST_URL required")
	}
	host, port, _ := sshTarget(t)
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	a.config.MP4URL = target
	a.config.MP4Token = os.Getenv("NT_MP4_TEST_TOKEN")
	a.config.MP4Directory = t.TempDir()
	asset := r04Asset(t, srv, admin, host, port, true)
	id, _ := reserve(t, srv, admin, asset, false)
	ctx := context.Background()
	if e := a.audit.Record(ctx, id, "output", 0, []byte("后台转换验收\r\n")); e != nil {
		t.Fatal(e)
	}
	if e := a.audit.Record(ctx, id, "output", 500, []byte("completed")); e != nil {
		t.Fatal(e)
	}
	a.finishSession(id, "closed", "test_completed")
	assertExportAfterDisconnect(t, a, srv, id, admin)
}

func assertExportAfterDisconnect(t *testing.T, a *App, srv *httptest.Server, id string, admin *http.Cookie) {
	t.Helper()
	ctx := context.Background()
	path := "/api/sessions/" + id + "/recording/mp4"
	expect(t, call(t, srv.URL, "POST", path, nil, admin, origin), 202)
	// The request has returned and its client is gone before work begins.
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { a.RunRecordingExports(workerCtx); close(done) }()
	defer func() { stop(); <-done }()
	until := time.Now().Add(60 * time.Second)
	for time.Now().Before(until) {
		state, e := a.audit.Export(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if state.State == "failed" {
			t.Fatalf("real renderer failed: %+v", state)
		}
		if state.State == "succeeded" {
			r := call(t, srv.URL, "GET", path+"/download", nil, admin, origin)
			expect(t, r, 200)
			if len(r.body) < 1000 || string(r.body[4:8]) != "ftyp" {
				t.Fatal("invalid real MP4")
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("real renderer timed out")
}

func TestMP4DurableJobPermissionsDedupeDownloadAndRetry(t *testing.T) {
	host, port, _ := sshTarget(t)
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	ownerID := postID(t, srv, "/api/admin/users", userInput("mp4-owner", nil, nil), admin)
	postID(t, srv, "/api/admin/grants", map[string]any{"userId": ownerID, "resourceId": asset}, admin)
	owner := loginAs(t, srv, "mp4-owner")
	id, _ := reserve(t, srv, owner, asset, false)
	path := "/api/sessions/" + id + "/recording/mp4"
	ctx := context.Background()
	a.config.MP4Directory = t.TempDir()
	a.config.MP4Token = strings.Repeat("m", 32)
	video := append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p'}, bytes.Repeat([]byte{1}, 64)...)
	received := false
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+a.config.MP4Token {
			t.Error("renderer authentication missing")
		}
		switch {
		case r.Method == "POST":
			var header map[string]any
			if json.NewDecoder(r.Body).Decode(&header) != nil || header["type"] != "header" {
				t.Error("missing snapshot")
			}
			received = true
			writeJSON(w, 202, map[string]string{"id": "11111111-1111-4111-8111-111111111111"})
		case r.Method == "DELETE":
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/result"):
			w.Write(video)
		default:
			writeJSON(w, 200, map[string]any{"state": "succeeded", "progress": 100})
		}
	}))
	defer renderer.Close()
	a.config.MP4URL = renderer.URL
	expect(t, call(t, srv.URL, "POST", path, nil, owner, origin), 409)
	if e := a.audit.Record(ctx, id, "output", 100, []byte("hello")); e != nil {
		t.Fatal(e)
	}
	a.finishSession(id, "closed", "test_completed")
	postID(t, srv, "/api/admin/users", userInput("mp4-denied", nil, nil), admin)
	denied := loginAs(t, srv, "mp4-denied")
	for _, method := range []string{"GET", "POST", "DELETE"} {
		expect(t, call(t, srv.URL, method, path, nil, denied, origin), 403)
		expect(t, call(t, srv.URL, method, path, nil, nil, origin), 401)
	}
	r := call(t, srv.URL, "POST", path, nil, owner, origin)
	expect(t, r, 202)
	first := object(t, r)["id"]
	r = call(t, srv.URL, "POST", path, nil, owner, origin)
	expect(t, r, 202)
	if object(t, r)["id"] != first {
		t.Fatal("duplicate job")
	}
	expect(t, call(t, srv.URL, "GET", path, nil, owner, origin), 200)
	q := jobs.New(a.db)
	job, e := q.Claim(ctx, "recording.mp4")
	if e != nil {
		t.Fatal(e)
	}
	a.runRecordingExport(ctx, job)
	state, e := a.audit.Export(ctx, id)
	if e != nil || state.State != "succeeded" || !received {
		t.Fatalf("conversion did not persist: %+v %v", state, e)
	}
	expect(t, call(t, srv.URL, "GET", path+"/download", nil, denied, origin), 403)
	download := call(t, srv.URL, "GET", path+"/download", nil, owner, origin)
	expect(t, download, 200)
	if !bytes.Equal(download.body, video) {
		t.Fatal("MP4 mismatch")
	}
	// Tampering invalidates the cache and permits an explicit retry.
	video[20] = 2
	if e = os.WriteFile(filepath.Join(a.config.MP4Directory, job.ID+".mp4"), video, 0600); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", path+"/download", nil, owner, origin), 409)
	retry := call(t, srv.URL, "POST", path, nil, owner, origin)
	expect(t, retry, 202)
	if object(t, retry)["id"] == first {
		t.Fatal("failed cache was reused")
	}
	expect(t, call(t, srv.URL, "DELETE", path, nil, owner, origin), 204)
	if _, e = q.Claim(ctx, "recording.mp4"); !jobs.IsEmpty(e) {
		t.Fatal("cancelled job claimable", e)
	}
	// A new worker observes durable cancellation, then recovers an expired lease.
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { a.RunRecordingExports(workerCtx); close(done) }()
	waitState := func(want string) {
		t.Helper()
		until := time.Now().Add(10 * time.Second)
		for time.Now().Before(until) {
			s, e := a.audit.Export(ctx, id)
			if e == nil && s.State == want {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("state never became %s", want)
	}
	waitState("cancelled")
	stop()
	<-done
	expect(t, call(t, srv.URL, "POST", path, nil, owner, origin), 202)
	lost, e := q.Claim(ctx, "recording.mp4")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.db.Exec(ctx, `UPDATE jobs SET lease_until=now()-interval '1 minute' WHERE id=$1`, lost.ID); e != nil {
		t.Fatal(e)
	}
	workerCtx, stop = context.WithCancel(ctx)
	done = make(chan struct{})
	go func() { a.RunRecordingExports(workerCtx); close(done) }()
	waitState("succeeded")
	stop()
	<-done
	// Cache expiry preserves the source; deleting source cancels pending conversion.
	_, _ = a.db.Exec(ctx, `UPDATE recording_exports SET updated_at=now()-interval '8 days' WHERE session_id=$1`, id)
	a.pruneRecordingExports(ctx)
	waitState("failed")
	if page, e := a.audit.Recording(ctx, id, 0); e != nil || page.Total != 1 {
		t.Fatal("cache expiry removed source", e)
	}
	expect(t, call(t, srv.URL, "POST", path, nil, owner, origin), 202)
	pending, e := a.audit.Export(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.db.Within(ctx, func(c context.Context) error { return a.audit.PurgeAccess(c, id) }); e != nil {
		t.Fatal(e)
	}
	var cancelled bool
	if e = a.db.Row(ctx, `SELECT cancel_requested FROM jobs WHERE id=$1`, pending.ID).Scan(&cancelled); e != nil || !cancelled {
		t.Fatal("retention failed to cancel export", e)
	}
}
