package application

import (
	"context"
	"fmt"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/identity"
	"sync"
	"testing"
)

func TestR16BooleanConfigurationAndIPLocks(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("ip-member", nil, nil), admin)
	update := func(patch map[string]any) {
		t.Helper()
		_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": patch}, admin)
		expect(t, r, 202)
	}
	for _, patch := range []map[string]any{{"login-lock-enabled": "FALSE"}, {"login-lock-enabled": 0}, {"login-lock-enabled": nil}, {"login-lock-ip-duration": nil}, {"login-lock-ip-duration": -1}, {"login-lock-ip-duration": 10081}, {"login-session-browser-close-logout": "yes"}} {
		_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": patch}, admin)
		expect(t, r, 422)
	}
	update(map[string]any{"login-lock-failed-times": 3, "login-lock-ip-duration": 7, "login-lock-account-duration": 0})
	for i := 0; i < 3; i++ {
		expect(t, call(t, srv.URL, "POST", "/api/login", map[string]string{"username": fmt.Sprintf("missing-%d", i), "password": "incorrect"}, nil, origin), 400)
	}
	expect(t, call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "ip-member", "password": testPassword}, nil, origin), 429)
	var failures int
	var seconds float64
	if err := pool.QueryRow(ctx, `SELECT failures,extract(epoch FROM locked_until-last_failed_at) FROM login_ip_locks WHERE address='127.0.0.1'`).Scan(&failures, &seconds); err != nil || failures != 3 || seconds != 420 {
		t.Fatal(failures, seconds, err)
	}
	locks := call(t, srv.URL, "GET", "/api/admin/login-locked", nil, admin, origin)
	expect(t, locks, 200)
	// The master switch pauses existing locks without deleting them. String false
	// must mean false, never the truthiness of a non-empty string.
	update(map[string]any{"login-lock-enabled": "false"})
	_ = loginAs(t, srv, "ip-member")
	update(map[string]any{"login-lock-enabled": true, "login-lock-ip-duration": 0})
	expect(t, call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "ip-member", "password": testPassword}, nil, origin), 429)
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/login-locked/ip:127.0.0.1", nil, admin, origin), 204)
	_ = loginAs(t, srv, "ip-member")
	update(map[string]any{"login-lock-ip-duration": 5})
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- a.identity.FailedPassword(ctx, fmt.Sprintf("ip-concurrent-%d", i), "::ffff:192.0.2.50")
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT failures FROM login_ip_locks WHERE address='192.0.2.50'`).Scan(&failures); err != nil || failures != 3 {
		t.Fatal("IP concurrent increments", failures, err)
	}
	if err := a.identity.CheckIPLock(ctx, "192.0.2.50"); err == nil {
		t.Fatal("mapped IPv4 evaded lock")
	}
	if err := a.identity.CheckIPLock(ctx, "192.0.2.51"); err != nil {
		t.Fatal("unrelated source blocked", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE login_ip_locks SET locked_until=now()-interval '1 second' WHERE address='192.0.2.50'`); err != nil {
		t.Fatal(err)
	}
	if err := a.identity.FailedPassword(ctx, "ip-after-expiry", "192.0.2.50"); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT failures FROM login_ip_locks WHERE address='192.0.2.50'`).Scan(&failures); err != nil || failures != 1 {
		t.Fatal("expired IP count not reset", failures, err)
	}
	if err := a.identity.UnlockIP(ctx, "bad-address"); err == nil {
		t.Fatal("invalid IP accepted")
	}
	// The direct peer address is authoritative; X-Forwarded-For is untrusted.
	req := httptest.NewRequest("POST", "/api/login", nil)
	req.RemoteAddr = "192.0.2.99:1234"
	req.Header.Set("X-Forwarded-For", "192.0.2.50")
	if sourceIP(req) != "192.0.2.99" {
		t.Fatal("trusted spoofed XFF")
	}
}

func TestR16BrowserSessionCookieSnapshot(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("cookie-member", nil, nil), admin)
	update := func(value any) {
		t.Helper()
		_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"login-session-browser-close-logout": value}}, admin)
		expect(t, r, 202)
	}
	update(true)
	cookie := loginAs(t, srv, "cookie-member")
	if cookie.MaxAge != 0 || !cookie.HttpOnly {
		t.Fatal("not a session cookie", cookie.MaxAge)
	}
	var seconds float64
	if err := pool.QueryRow(ctx, `SELECT extract(epoch FROM absolute_expires_at-issued_at) FROM auth_sessions WHERE token_hash=$1`, identity.Digest(cookie.Value)).Scan(&seconds); err != nil || seconds != 480*60 {
		t.Fatal("server expiration changed", seconds, err)
	}
	update("false")
	age, err := a.identity.CookieLifetime(ctx, cookie.Value)
	if err != nil || age != 0 {
		t.Fatal("cookie preference not captured", age, err)
	}
	persistent := loginAs(t, srv, "cookie-member")
	assertCookieDuration(t, persistent, 480*60)
	if _, err = pool.Exec(ctx, `UPDATE auth_sessions SET absolute_expires_at=now()-interval '1 second' WHERE token_hash=$1`, identity.Digest(cookie.Value)); err != nil {
		t.Fatal(err)
	}
	if _, err = a.identity.CookieLifetime(ctx, cookie.Value); err == nil {
		t.Fatal("expired browser cookie accepted")
	}
}
