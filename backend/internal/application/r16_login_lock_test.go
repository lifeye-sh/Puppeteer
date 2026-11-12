package application

import (
	"context"
	"errors"
	"next-terminal/backend/internal/platform/fault"
	"sync"
	"testing"
	"time"
)

func TestR16LoginLockConfiguration(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("lock-fixture", nil, nil), admin)
	update := func(patch map[string]int) {
		t.Helper()
		_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": patch}, admin)
		expect(t, r, 202)
	}
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	for _, patch := range []map[string]int{{"login-lock-failed-times": 0}, {"login-lock-failed-times": 101}, {"login-lock-failed-duration": 0}, {"login-lock-failed-duration": 1441}, {"login-lock-account-duration": -1}, {"login-lock-account-duration": 10081}} {
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": patch}, admin)
		expect(t, r, 422)
	}
	update(map[string]int{"login-lock-failed-duration": 2, "login-lock-failed-times": 3, "login-lock-account-duration": 7})
	expectedConfig, configErr := a.system.Configuration(ctx, 0)
	if configErr != nil {
		t.Fatal(configErr)
	}
	fail := func(name string) {
		t.Helper()
		if e := a.identity.FailedPassword(ctx, name); e != nil {
			t.Fatal(e)
		}
	}
	check := func(name string) error {
		return a.db.Within(ctx, func(c context.Context) error { return a.identity.CheckLock(c, name) })
	}
	for i := 0; i < 3; i++ {
		expect(t, call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "lock-fixture", "password": "wrong"}, nil, origin), 400)
	}
	expect(t, call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "lock-fixture", "password": testPassword}, nil, origin), 429)
	var failures int
	var until time.Time
	var seconds float64
	var version int64
	if e := pool.QueryRow(ctx, `SELECT failures,locked_until,extract(epoch FROM locked_until-last_failed_at),configuration_revision FROM login_locks WHERE username='lock-fixture'`).Scan(&failures, &until, &seconds, &version); e != nil || failures != 3 || seconds != 420 || version != expectedConfig.ID {
		t.Fatal("custom lock", failures, seconds, version, e)
	}
	// Disabling creation of new locks never unlocks an already locked account.
	update(map[string]int{"login-lock-account-duration": 0})
	fail("lock-fixture")
	var kept time.Time
	if e := pool.QueryRow(ctx, `SELECT locked_until FROM login_locks WHERE username='lock-fixture'`).Scan(&kept); e != nil || !kept.Equal(until) {
		t.Fatal("active lock changed", e)
	}
	if !errors.Is(check("lock-fixture"), fault.Locked) {
		t.Fatal("existing lock bypassed")
	}
	for i := 0; i < 5; i++ {
		fail("not-locked")
	}
	if e := check("not-locked"); e != nil {
		t.Fatal("zero-duration created lock", e)
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/login-locked/lock-fixture", nil, admin, origin), 204)
	_ = loginAs(t, srv, "lock-fixture")
	update(map[string]int{"login-lock-account-duration": 7})
	// Window expiry resets; a recent failure extends the inactivity window.
	fail("window-fixture")
	fail("window-fixture")
	if _, e := pool.Exec(ctx, `UPDATE login_locks SET last_failed_at=now()-interval '3 minutes' WHERE username='window-fixture'`); e != nil {
		t.Fatal(e)
	}
	fail("window-fixture")
	if e := pool.QueryRow(ctx, `SELECT failures FROM login_locks WHERE username='window-fixture'`).Scan(&failures); e != nil || failures != 1 {
		t.Fatal("window reset", failures, e)
	}
	fail("window-fixture")
	fail("window-fixture")
	if _, e := pool.Exec(ctx, `UPDATE login_locks SET locked_until=now()-interval '1 second' WHERE username='window-fixture'`); e != nil {
		t.Fatal(e)
	}
	fail("window-fixture")
	if e := check("window-fixture"); e != nil {
		t.Fatal("expired lock instantly re-locked", e)
	}
	if e := pool.QueryRow(ctx, `SELECT failures FROM login_locks WHERE username='window-fixture'`).Scan(&failures); e != nil || failures != 1 {
		t.Fatal("post-lock counter", failures, e)
	}
	// Independent concurrent transactions stop counting once threshold is reached.
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- a.identity.FailedPassword(ctx, "parallel-fixture") }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if e := pool.QueryRow(ctx, `SELECT failures FROM login_locks WHERE username='parallel-fixture'`).Scan(&failures); e != nil || failures != 3 {
		t.Fatal("concurrent count", failures, e)
	}
	if !errors.Is(check("parallel-fixture"), fault.Locked) {
		t.Fatal("concurrent lock absent")
	}
	update(map[string]int{"login-lock-failed-times": 100})
	errs = make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- a.identity.FailedPassword(ctx, "count-fixture") }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if e := pool.QueryRow(ctx, `SELECT failures FROM login_locks WHERE username='count-fixture'`).Scan(&failures); e != nil || failures != 16 {
		t.Fatal("lost concurrent increment", failures, e)
	}
	update(map[string]int{"login-lock-failed-times": 3})
	// A successful password login removes a partial failure counter.
	fail("lock-fixture")
	_ = loginAs(t, srv, "lock-fixture")
	var count int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM login_locks WHERE username='lock-fixture'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("success did not reset", e)
	}
	// Lowering a threshold takes effect on the next failed check, not by rewriting old rows.
	fail("tighten-fixture")
	update(map[string]int{"login-lock-failed-times": 1})
	if e := check("tighten-fixture"); e != nil {
		t.Fatal(e)
	}
	fail("tighten-fixture")
	if !errors.Is(check("tighten-fixture"), fault.Locked) {
		t.Fatal("new policy ignored")
	}
	historical, e := a.system.Configuration(ctx, 2)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := historical.Values["login-lock-failed-times"]; ok {
		t.Fatal("migration rewrote history")
	}
	// Fault injection is confined to this disposable schema: a missing zero-capable key must not disable protection.
	if _, e := pool.Exec(ctx, `UPDATE configuration_revisions SET snapshot=snapshot-'login-lock-account-duration' WHERE id=(SELECT revision FROM configuration_head)`); e != nil {
		t.Fatal(e)
	}
	if !errors.Is(a.identity.FailedPassword(ctx, "invalid-policy-fixture"), fault.Invalid) {
		t.Fatal("incomplete policy accepted")
	}
}
