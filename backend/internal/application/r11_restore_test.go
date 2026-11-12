package application

import (
	"context"
	"testing"
)

func TestR11RestoreQuarantine(t *testing.T) {
	p, a, srv := r02Server(t, false)
	ctx := context.Background()
	cookie := loginAs(t, srv, "accept-admin")
	hostBefore, e := a.nativeGatewayKey(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var epoch int64
	if e := p.QueryRow(ctx, "SELECT auth_epoch FROM instance_state").Scan(&epoch); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `INSERT INTO jobs(kind,state) VALUES('ssh.command','queued'),('credential.rotate','running'),('ssh.command','succeeded')`); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `UPDATE native_forward_settings SET enabled=true`); e != nil {
		t.Fatal(e)
	}
	var streamID string
	if e := p.QueryRow(ctx, `WITH asset AS (INSERT INTO resource_registry(kind,display_name) VALUES('asset','restore forward fixture') RETURNING id), live AS (INSERT INTO sessions(actor_id,asset_id,asset_version,credential_version,host_key_version,readonly,state,ticket_hash,auth_hash,ticket_expires_at,lease_expires_at,native_forward) SELECT u.id,a.id,1,1,1,false,'connected','','',now()+interval '1 minute',now()+interval '1 minute',true FROM users u CROSS JOIN asset a WHERE u.username='accept-admin' RETURNING id) INSERT INTO native_forward_streams(session_id,host,port,address,state) SELECT id,'localhost',443,'127.0.0.1','connected' FROM live RETURNING id`).Scan(&streamID); e != nil {
		t.Fatal(e)
	}
	if e := a.QuarantineRestoredState(ctx, "restore-test-fixture"); e != nil {
		t.Fatal(e)
	}
	var forwardEnabled bool
	var streamClosed bool
	if e := p.QueryRow(ctx, `SELECT state='interrupted' AND ended_at IS NOT NULL FROM native_forward_streams WHERE id=$1`, streamID).Scan(&streamClosed); e != nil || !streamClosed {
		t.Fatal("restored forwarding stream remained active", e)
	}
	if e := p.QueryRow(ctx, `SELECT enabled FROM native_forward_settings`).Scan(&forwardEnabled); e != nil || forwardEnabled {
		t.Fatal("restored forwarding enabled", e)
	}
	hostAfter, e := a.nativeGatewayKey(ctx)
	if e != nil || string(hostBefore.PublicKey().Marshal()) == string(hostAfter.PublicKey().Marshal()) {
		t.Fatal("restored SSH gateway reused its old host key", e)
	}
	var after int64
	var pending, unknown, succeeded, revoked int
	if e := p.QueryRow(ctx, "SELECT auth_epoch FROM instance_state").Scan(&after); e != nil {
		t.Fatal(e)
	}
	if after != epoch+1 {
		t.Fatal("auth epoch not advanced")
	}
	p.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE state IN ('queued','running')").Scan(&pending)
	p.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE state='unknown'").Scan(&unknown)
	p.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE state='succeeded'").Scan(&succeeded)
	p.QueryRow(ctx, "SELECT count(*) FROM auth_sessions WHERE revoked_at IS NOT NULL").Scan(&revoked)
	if pending != 0 || unknown != 2 || succeeded != 1 || revoked < 1 {
		t.Fatalf("quarantine counts: %d %d %d %d", pending, unknown, succeeded, revoked)
	}
	expect(t, call(t, srv.URL, "GET", "/api/admin/access-retention", nil, cookie, origin), 401)
	// No old job becomes eligible when the normal recovery worker runs.
	if e := a.RecoverTaskJobs(ctx); e != nil {
		t.Fatal(e)
	}
	p.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE state IN ('queued','running')").Scan(&pending)
	if pending != 0 {
		t.Fatal("old jobs replayed")
	}
}
