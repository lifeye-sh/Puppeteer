package application

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/modules/vault"
	"next-terminal/backend/internal/platform/jobs"
	"strings"
	"testing"
)

func TestR10RotationIntentRecoveryAndFencing(t *testing.T) {
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	host, port, _ := taskSSH(t)
	asset := r04Asset(t, srv, admin, host, port, true)
	body := map[string]any{"key": taskKey, "assetIds": []string{asset}}
	res := call(t, srv.URL, "POST", "/api/rotation-runs", body, admin, origin)
	expect(t, res, 201)
	id := object(t, res)["id"].(string)
	duplicate := call(t, srv.URL, "POST", "/api/rotation-runs", body, admin, origin)
	expect(t, duplicate, 201)
	if object(t, duplicate)["id"] != id {
		t.Fatal("request was replayed")
	}
	body["key"] = "20000000-0000-4000-8000-000000000002"
	expect(t, call(t, srv.URL, "POST", "/api/rotation-runs", body, admin, origin), 409)
	ctx := context.Background()
	run, e := a.vault.RotationRun(ctx, id)
	if e != nil || len(run.Targets) != 1 {
		t.Fatal(run, e)
	}
	raw, _ := json.Marshal(run)
	if strings.Contains(string(raw), "candidate") || strings.Contains(string(raw), "accountKey") {
		t.Fatal("private fields leaked")
	}
	q := jobs.New(a.db)
	job, e := q.Claim(ctx, "credential.rotate")
	if e != nil {
		t.Fatal(e)
	}
	target := run.Targets[0]
	e = a.db.Within(ctx, func(c context.Context) error {
		return a.vault.MoveRotation(c, job, target.ID, vault.RotationPending, vault.RotationApplying, "")
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if e = a.RecoverTaskJobs(ctx); e != nil {
		t.Fatal(e)
	}
	run, e = a.vault.RotationRun(ctx, id)
	if e != nil || run.Targets[0].State != vault.RotationUnknown || run.State != "unknown" {
		t.Fatal(run, e)
	}
	e = a.db.Within(ctx, func(c context.Context) error {
		return a.vault.MoveRotation(c, job, target.ID, vault.RotationApplying, vault.RotationVerifying, "")
	})
	if e == nil {
		t.Fatal("stale worker accepted")
	}
	expect(t, call(t, srv.URL, "POST", "/api/rotation-runs", body, admin, origin), 409)
	// Pending cancellation releases the target lock, while dispatched unknown does not.
	if _, e = p.Exec(ctx, `UPDATE rotation_targets SET state='failed' WHERE id=$1`, target.ID); e != nil {
		t.Fatal(e)
	}
	res = call(t, srv.URL, "POST", "/api/rotation-runs", body, admin, origin)
	expect(t, res, 201)
	next := object(t, res)["id"].(string)
	expect(t, call(t, srv.URL, "POST", "/api/rotation-runs/"+next+"/cancel", nil, admin, origin), 204)
	if e = a.RecoverTaskJobs(ctx); e != nil {
		t.Fatal(e)
	}
	run, e = a.vault.RotationRun(ctx, next)
	if e != nil || run.Targets[0].State != vault.RotationCancelled {
		t.Fatal(run, e)
	}
}

func TestR10RotationRejectsFalseAuthenticationEvidence(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	host, port, _ := taskSSH(t)
	asset := r04Asset(t, srv, admin, host, port, true)
	res := call(t, srv.URL, "POST", "/api/rotation-runs", map[string]any{"key": taskKey, "assetIds": []string{asset}}, admin, origin)
	expect(t, res, 201)
	id := object(t, res)["id"].(string)
	if e := a.RunTaskOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	run, e := a.vault.RotationRun(context.Background(), id)
	if e != nil || run.State != "unknown" || run.Targets[0].NewCredentialID != "" {
		t.Fatal(run, e)
	}
	result := call(t, srv.URL, "POST", "/api/rotation-runs/"+id+"/targets/"+run.Targets[0].ID+"/reconcile", nil, admin, origin)
	expect(t, result, 200)
	if object(t, result)["state"] != "unknown" {
		t.Fatal("NoClientAuth incorrectly accepted as rotation evidence")
	}
	postID(t, srv, "/api/admin/users", userInput("rotation-viewer", nil, nil), admin)
	other := loginAs(t, srv, "rotation-viewer")
	expect(t, call(t, srv.URL, "GET", "/api/rotation-runs/"+id, nil, other, origin), 403)
	expect(t, call(t, srv.URL, "POST", "/api/rotation-runs/"+id+"/cancel", nil, other, origin), 403)
}

func TestR10RotationRecoversPersistedCompletion(t *testing.T) {
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	host, port, _ := taskSSH(t)
	asset := r04Asset(t, srv, admin, host, port, true)
	res := call(t, srv.URL, "POST", "/api/rotation-runs", map[string]any{"key": taskKey, "assetIds": []string{asset}}, admin, origin)
	expect(t, res, 201)
	id := object(t, res)["id"].(string)
	ctx := context.Background()
	if _, e := jobs.New(a.db).Claim(ctx, "credential.rotate"); e != nil {
		t.Fatal(e)
	}
	// Target commit persisted, but worker lost the reply before finalizing the job.
	if _, e := p.Exec(ctx, `UPDATE rotation_targets SET state='succeeded' WHERE run_id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `UPDATE jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if e := a.RecoverTaskJobs(ctx); e != nil {
		t.Fatal(e)
	}
	run, e := a.vault.RotationRun(ctx, id)
	if e != nil || run.State != "succeeded" {
		t.Fatal(run, e)
	}
}
