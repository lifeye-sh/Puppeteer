package application

import (
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"next-terminal/backend/internal/modules/automation"
	"next-terminal/backend/internal/modules/entitlements"
	"next-terminal/backend/internal/modules/notification"
	"next-terminal/backend/internal/modules/storage"
	"next-terminal/backend/internal/platform/jobs"
	"next-terminal/backend/internal/platform/postgres"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type businessLicenseFixture struct{ denied atomic.Bool }

func (p *businessLicenseFixture) Snapshot(instance string, now time.Time) (entitlements.Snapshot, error) {
	features := map[string]bool{}
	for _, k := range entitlements.ControlledFeatures {
		features[k] = true
	}
	if p.denied.Load() {
		for _, k := range []string{"automation.execute", "notification.send", "files.write"} {
			features[k] = false
		}
	}
	return entitlements.Snapshot{InstanceID: instance, Source: "local", State: "valid", Features: features}, nil
}

func TestR16TaskEntitlementDispatchCronAndRevocation(t *testing.T) {
	pool, base, original := r02Server(t, false)
	admin := loginAs(t, original, "accept-admin")
	policy := &businessLicenseFixture{}
	cfg := base.config
	cfg.EntitlementLocal = policy
	app := New(pool, cfg)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	ctx := context.Background()
	host, port, count := taskSSH(t)
	asset := r04Asset(t, srv, admin, host, port, true)
	id := postID(t, srv, "/api/tasks", r10TaskInput(asset, "ok"), admin)
	queued := triggerTask(t, srv, admin, id, taskKey)
	policy.denied.Store(true)
	expect(t, call(t, srv.URL, "POST", "/api/tasks/"+id+"/trigger", map[string]any{"key": taskUUID(16901), "version": 1}, admin, origin), 403)
	if again := triggerTask(t, srv, admin, id, taskKey); again != queued {
		t.Fatal("idempotent history blocked")
	}
	enabled := r10TaskInput(asset, "ok")
	enabled["enabled"] = true
	enabled["cron"] = "* * * * *"
	expect(t, call(t, srv.URL, "POST", "/api/tasks", enabled, admin, origin), 403)
	if e := app.RunTaskOnce(ctx); e != nil {
		t.Fatal(e)
	}
	svc := automation.New(app.db)
	run, e := svc.Run(ctx, queued)
	if e != nil || run.State != "failed" || count.Load() != 0 {
		t.Fatal(run.Job, count.Load(), e)
	}
	// A missed scheduled occurrence becomes a durable skipped run, never delayed execution.
	var cronID string
	e = app.db.Within(ctx, func(c context.Context) error {
		v, e := svc.Schedule(c, id, true)
		if e != nil {
			return e
		}
		at := time.Now()
		cronID, e = svc.Trigger(c, jobs.New(app.db), v, "cron:entitlement", &at, "")
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	cron, e := svc.Run(ctx, cronID)
	if e != nil || cron.State != "skipped" || cron.Reason != "entitlement_unavailable" {
		t.Fatal(cron.Job, e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/tasks", nil, admin, origin), 200)
	policy.denied.Store(false)
	in := r10TaskInput(asset, "sleep")
	in["timeoutSeconds"] = 10
	runningID := postID(t, srv, "/api/tasks", in, admin)
	rid := triggerTask(t, srv, admin, runningID, taskKey)
	done := make(chan error, 1)
	go func() { done <- app.RunTaskOnce(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for count.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if count.Load() != 1 {
		t.Fatal("command did not start")
	}
	policy.denied.Store(true)
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revoked task not closed")
	}
	run, e = svc.Run(ctx, rid)
	if e != nil || run.State != "unknown" || len(run.Targets) != 1 || run.Targets[0].Reason != "authorization_revoked_result_unknown" {
		t.Fatal(run, e)
	}
	if e = app.RunTaskOnce(ctx); e != nil || count.Load() != 1 {
		t.Fatal("unknown task replayed", e, count.Load())
	}
}

func TestR16NotificationEntitlementNoDispatchAndInflight(t *testing.T) {
	pool, base, original := r02Server(t, false)
	admin := loginAs(t, original, "accept-admin")
	policy := &businessLicenseFixture{}
	cfg := base.config
	cfg.EntitlementLocal = policy
	app := New(pool, cfg)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	ctx := context.Background()
	user, e := app.identity.FindByName(ctx, "accept-admin")
	if e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	var hold atomic.Bool
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	vendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		if hold.Load() {
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer func() { close(release); vendor.CloseClientConnections(); vendor.Close() }()
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: vendor.TLS.Certificates[0].Certificate[0]}))
	channel := postID(t, srv, "/api/admin/notifications/channels", notification.Channel{Name: "entitlement SMS", Type: "sms-webhook", Enabled: true, Secret: "fixture-secret", Config: notification.Config{APIBase: vendor.URL, CA: ca}}, admin)
	postID(t, srv, "/api/admin/notifications/bindings", notification.Binding{UserID: user.ID, ChannelID: channel, Address: "+12025550123", Enabled: true}, admin)
	svc := app.notifications()
	queued, e := svc.Test(ctx, channel, user.ID)
	if e != nil {
		t.Fatal(e)
	}
	policy.denied.Store(true)
	expect(t, call(t, srv.URL, "POST", "/api/admin/notifications/channels/"+channel+"/test", nil, admin, origin), 403)
	if e = svc.Retry(ctx, queued); !errors.Is(e, postgres.ErrAdmission) {
		t.Fatal(e)
	}
	if e = svc.DeliverOne(ctx, nil); e != nil {
		t.Fatal(e)
	}
	rows, e := svc.Deliveries(ctx)
	if e != nil || len(rows) != 1 || rows[0].Status != "skipped" || calls.Load() != 0 {
		t.Fatal(rows, e, calls.Load())
	}
	// Account recovery/verification is essential and not a business notification.
	if e = svc.SendVerification(ctx, channel, 1, "+12025550123", taskUUID(16902), "fixture-code"); e != nil || calls.Load() != 1 {
		t.Fatal("verification blocked", e)
	}
	policy.denied.Store(false)
	hold.Store(true)
	_, e = svc.Test(ctx, channel, user.ID)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- svc.DeliverOne(ctx, nil) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not reached")
	}
	policy.denied.Store(true)
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider not cancelled")
	}
	rows, e = svc.Deliveries(ctx)
	if e != nil || rows[0].Status != "skipped" || !strings.Contains(rows[0].Error, "result unknown") {
		t.Fatal(rows, e)
	}
	policy.denied.Store(false)
	if e = svc.DeliverOne(ctx, nil); e != nil || calls.Load() != 2 {
		t.Fatal("revoked send replayed", e, calls.Load())
	}
}

func TestR16FileEntitlementSFTPAndSharedDrive(t *testing.T) {
	pool, base, original := r02Server(t, false)
	admin := loginAs(t, original, "accept-admin")
	policy := &businessLicenseFixture{}
	cfg := base.config
	cfg.EntitlementLocal = policy
	app := New(pool, cfg)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	ctx := context.Background()
	user, e := app.identity.FindByName(ctx, "accept-admin")
	if e != nil {
		t.Fatal(e)
	}
	host, port, _ := sshTargetRoot(t, t.TempDir())
	asset := r04Asset(t, srv, admin, host, port, true)
	sid, ticket := reserve(t, srv, admin, asset, false)
	ws := attach(t, srv, admin, sid, ticket)
	defer ws.Close()
	readUntil(t, ws, "3"+sid)
	remote := t.TempDir()
	basePath := "/api/sessions/" + sid
	expect(t, call(t, srv.URL, "POST", basePath+"/files/actions", map[string]string{"action": "mkdir", "path": remote + "/before"}, admin, origin), 200)
	upload := taskUUID(16910)
	expect(t, call(t, srv.URL, "POST", basePath+"/uploads", taskInput(upload, remote+"/pending", 1, ""), admin, origin), 200)
	svc := storage.New(app.db)
	actor := storage.WithActor(ctx, user.ID, "web", "")
	root, e := svc.Ensure(actor, user.ID)
	if e != nil {
		t.Fatal(e)
	}
	file, e := svc.Create(actor, user.ID, root.ID, "before.txt", false)
	if e != nil {
		t.Fatal(e)
	}
	if e = svc.Write(actor, user.ID, file.ID, 0, []byte("retained")); e != nil {
		t.Fatal(e)
	}
	task, e := svc.StartUpload(actor, user.ID, root.ID, "pending", 10)
	if e != nil {
		t.Fatal(e)
	}
	policy.denied.Store(true)
	expect(t, call(t, srv.URL, "POST", basePath+"/files/actions", map[string]string{"action": "mkdir", "path": remote + "/blocked"}, admin, origin), 403)
	expect(t, putChunk(t, srv, admin, upload, 0, []byte("x")), 403)
	expect(t, call(t, srv.URL, "GET", basePath+"/files?path="+url.QueryEscape(remote), nil, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/uploads/"+upload+"/cancel", nil, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/storage/folders", map[string]any{"parentId": root.ID, "name": "blocked"}, admin, origin), 403)
	for _, write := range []func() error{
		func() error { return svc.Write(actor, user.ID, file.ID, 0, []byte("changed")) },
		func() error { return svc.Resize(actor, user.ID, file.ID, 0) },
		func() error { return svc.Rename(actor, user.ID, file.ID, root.ID, "changed") },
		func() error { return svc.Remove(actor, user.ID, file.ID) },
		func() error { return svc.UploadPart(actor, task.ID, 0, []byte("x")) },
	} {
		if e = write(); !errors.Is(e, syscall.EACCES) {
			t.Fatal("write allowed", e)
		}
	}
	// The independent mount process uses its own DB wrapper and the same licensing service.
	mountDB := postgres.New(pool)
	mountDB.SetAdmissionCheck(entitlements.NewService(mountDB, policy, nil).Admit)
	mount := storage.New(mountDB)
	rdpActor := storage.WithActor(ctx, user.ID, "rdp", sid)
	if e = mount.Write(rdpActor, user.ID, file.ID, 0, []byte("x")); !errors.Is(e, syscall.EACCES) {
		t.Fatal("mount bypass", e)
	}
	content, e := mount.Read(rdpActor, user.ID, file.ID, 0, 100)
	if e != nil || string(content) != "retained" {
		t.Fatal("read/export blocked", e, string(content))
	}
	if e = svc.CancelUpload(actor, task.ID); e != nil {
		t.Fatal("cancel blocked", e)
	}
	usage, e := svc.Usage(actor, user.ID)
	if e != nil || usage.Used != 8 {
		t.Fatal("quota not released", usage, e)
	}
}
