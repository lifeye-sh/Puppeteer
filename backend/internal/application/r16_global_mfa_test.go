package application

import (
	"context"
	"next-terminal/backend/internal/modules/identity"
	"testing"
)

func TestR16GlobalMFARequiresVerifiedLogin(t *testing.T) {
	pool, _, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("force-new", nil, nil), admin)
	postID(t, srv, "/api/admin/users", userInput("force-bound", nil, nil), admin)
	unbound := loginAs(t, srv, "force-new")
	bound := loginAs(t, srv, "force-bound")
	_, _, bound = enroll(t, srv, bound)
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"login-force-totp-enabled": true}}, admin)
	expect(t, r, 202)
	// Existing sessions without completed MFA immediately lose business access.
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, unbound, origin), 401)
	if got := object(t, call(t, srv.URL, "GET", "/api/login-status", nil, unbound, origin))["status"]; got != "MFA Enrollment Required" {
		t.Fatal(got)
	}
	expect(t, call(t, srv.URL, "POST", "/api/account/password", map[string]string{}, unbound, origin), 403)
	newLogin := loginAs(t, srv, "force-new")
	assertCookieDuration(t, newLogin, 300)
	_, _, ready := enroll(t, srv, newLogin)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, ready, origin), 200)
	status := object(t, call(t, srv.URL, "GET", "/api/account/mfa", nil, ready, origin))
	if status["enabled"] != true || status["required"] != false {
		t.Fatal("completed global MFA still requests enrollment", status)
	}
	var recorded bool
	if e := pool.QueryRow(ctx, `SELECT mfa_verified_at IS NOT NULL FROM auth_sessions WHERE token_hash=$1`, identity.Digest(ready.Value)).Scan(&recorded); e != nil || !recorded {
		t.Fatal("MFA completion not persisted", e)
	}
	// Previously verified sessions stay valid, but upgraded sessions with no
	// trusted completion timestamp must complete TOTP again.
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, bound, origin), 200)
	if _, e := pool.Exec(ctx, `UPDATE auth_sessions SET mfa_verified_at=NULL WHERE token_hash=$1`, identity.Digest(bound.Value)); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, bound, origin), 401)
	if got := object(t, call(t, srv.URL, "GET", "/api/login-status", nil, bound, origin))["status"]; got != "OTP Required" {
		t.Fatal(got)
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/password", nil, bound, origin), 403)
}
