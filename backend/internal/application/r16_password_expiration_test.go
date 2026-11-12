package application

import (
	"context"
	"next-terminal/backend/internal/modules/identity"
	"testing"
	"time"
)

func TestR16PasswordExpirationAndSelfChange(t *testing.T) {
	pool, _, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	uid := postID(t, srv, "/api/admin/users", userInput("expiry-user", nil, nil), admin)
	owner := loginAs(t, srv, "expiry-user")
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	for _, days := range []int{-2, 0, 3651} {
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"password-expiration-period": days}}, admin)
		expect(t, r, 422)
	}
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"password-expiration-period": 1}}, admin)
	expect(t, r, 202)
	if _, e := pool.Exec(ctx, `UPDATE users SET password_changed_at=now()-interval '2 days' WHERE id=$1`, uid); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, owner, origin), 401)
	status := object(t, call(t, srv.URL, "GET", "/api/login-status", nil, owner, origin))
	if status["status"] != "Password Change Required" {
		t.Fatal(status)
	}
	var remaining float64
	var phase string
	if e := pool.QueryRow(ctx, `SELECT phase,extract(epoch FROM absolute_expires_at-now())::float8 FROM auth_sessions WHERE token_hash=$1`, identity.Digest(owner.Value)).Scan(&phase, &remaining); e != nil || phase != "password_change" || remaining > 300 {
		t.Fatal(phase, remaining, e)
	}
	for _, path := range []string{"/api/account/mfa", "/api/admin/users/paging"} {
		want := 403
		if path == "/api/admin/users/paging" {
			want = 401
		}
		expect(t, call(t, srv.URL, "GET", path, nil, owner, origin), want)
	}
	newLogin := loginAs(t, srv, "expiry-user")
	assertCookieDuration(t, newLogin, 300)
	expect(t, call(t, srv.URL, "POST", "/api/account/mfa/enroll", map[string]string{"password": testPassword}, newLogin, origin), 403)
	change := map[string]string{"password": "wrong-password", "newPassword": "fresh safe phrase 2026!", "confirm": "fresh safe phrase 2026!"}
	expect(t, call(t, srv.URL, "POST", "/api/account/password", change, owner, origin), 400)
	change["password"] = testPassword
	expect(t, call(t, srv.URL, "POST", "/api/account/password", change, owner, origin), 204)
	expect(t, call(t, srv.URL, "GET", "/api/account/password", nil, owner, origin), 401)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, newLogin, origin), 401)
	expect(t, call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "expiry-user", "password": testPassword}, nil, origin), 400)
	fresh := call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "expiry-user", "password": change["newPassword"]}, nil, origin)
	expect(t, fresh, 200)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, fresh.cookie, origin), 200)
	var changed time.Time
	if e := pool.QueryRow(ctx, `SELECT password_changed_at FROM users WHERE id=$1`, uid).Scan(&changed); e != nil || time.Since(changed) > time.Minute {
		t.Fatal("password clock not updated", e)
	}
	var logged int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM operation_logs WHERE action='identity.password_changed'`).Scan(&logged); e != nil || logged != 1 {
		t.Fatal(logged, e)
	}
}

func TestR16PasswordChangeRequiresFreshMFAProof(t *testing.T) {
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("password-mfa", nil, nil), admin)
	owner := loginAs(t, srv, "password-mfa")
	secret, _, owner := enroll(t, srv, owner)
	status := object(t, call(t, srv.URL, "GET", "/api/account/password", nil, owner, origin))
	if status["mfaRequired"] != true {
		t.Fatal(status)
	}
	change := map[string]string{"password": testPassword, "newPassword": "updated long phrase 2026!", "confirm": "updated long phrase 2026!"}
	expect(t, call(t, srv.URL, "POST", "/api/account/password", change, owner, origin), 400)
	// Enrollment already consumed this step: possession of the browser session
	// does not allow reuse of its original TOTP to change credentials.
	change["totp"] = identity.TOTP(secret, time.Now().Unix()/30)
	expect(t, call(t, srv.URL, "POST", "/api/account/password", change, owner, origin), 400)
	change["totp"] = identity.TOTP(secret, time.Now().Unix()/30+1)
	expect(t, call(t, srv.URL, "POST", "/api/account/password", change, owner, origin), 204)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, owner, origin), 401)
	login := call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "password-mfa", "password": change["newPassword"]}, nil, origin)
	expect(t, login, 200)
	if got := object(t, call(t, srv.URL, "GET", "/api/login-status", nil, login.cookie, origin))["status"]; got != "OTP Required" {
		t.Fatal("password change removed MFA", got)
	}
}
