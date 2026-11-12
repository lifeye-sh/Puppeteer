package application

import (
	"context"
	"testing"
)

func TestR16RestoreServicesOwnAtomicTransaction(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	if _, e := a.nativeGatewayKey(ctx); e != nil {
		t.Fatal(e)
	}
	// Simulate a late storage failure, after gateway identity deletion. Public
	// restore entry points must roll back every preceding mutation themselves.
	if _, e := pool.Exec(ctx, `CREATE FUNCTION r16_restore_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'isolated restore failure'; END $$; CREATE TRIGGER r16_restore_fail BEFORE UPDATE ON auth_sessions FOR EACH STATEMENT EXECUTE FUNCTION r16_restore_fail()`); e != nil {
		t.Fatal(e)
	}
	if e := a.identity.QuarantineRestore(ctx); e == nil {
		t.Fatal("failure not propagated")
	}
	if _, e := pool.Exec(ctx, `DROP TRIGGER r16_restore_fail ON auth_sessions`); e != nil {
		t.Fatal(e)
	}
	var keys int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM native_ssh_gateway_keys`).Scan(&keys); e != nil || keys != 1 {
		t.Fatal("partial identity quarantine", keys, e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, admin, origin), 200)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": true, "shellEnabled": true, "version": 1}, admin, origin), 200)
	before, e := a.system.Configuration(ctx, 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `CREATE TRIGGER r16_restore_fail BEFORE UPDATE ON ai_turns FOR EACH STATEMENT EXECUTE FUNCTION r16_restore_fail()`); e != nil {
		t.Fatal(e)
	}
	if e = a.aiService().QuarantineRestore(ctx); e == nil {
		t.Fatal("AI failure not propagated")
	}
	if _, e = pool.Exec(ctx, `DROP TRIGGER r16_restore_fail ON ai_turns; DROP FUNCTION r16_restore_fail()`); e != nil {
		t.Fatal(e)
	}
	after, e := a.system.Configuration(ctx, 0)
	if e != nil || after.ID != before.ID {
		t.Fatal("partial AI configuration quarantine", e)
	}
	policy, e := a.aiService().Settings(ctx)
	if e != nil || !policy.Enabled || !policy.ShellEnabled {
		t.Fatal("partial AI quarantine", e)
	}
	if e = a.aiService().QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	if e = a.identity.QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, admin, origin), 401)
}
