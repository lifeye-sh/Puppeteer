package application

import (
	"context"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/identity"
	"testing"
	"time"
)

func changeLoginDuration(t *testing.T, srv *httptest.Server, admin *http.Cookie, minutes int) {
	t.Helper()
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]int{"login-session-duration": minutes}}, admin)
	expect(t, r, 202)
}
func assertCookieDuration(t *testing.T, c *http.Cookie, seconds int) {
	t.Helper()
	if c == nil {
		t.Fatal("missing session cookie")
	}
	if c.MaxAge < seconds-5 || c.MaxAge > seconds || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("expected %ds; cookie name=%s maxAge=%d httpOnly=%v sameSite=%d", seconds, c.Name, c.MaxAge, c.HttpOnly, c.SameSite)
	}
}
func TestR16LoginDuration(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	const secureOrigin = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, secureOrigin)
	admin := loginAs(t, srv, "accept-admin")
	assertCookieDuration(t, admin, 480*60)
	userID := postID(t, srv, "/api/admin/users", userInput("duration-user", nil, nil), admin)
	var oldExpiry time.Time
	if e := pool.QueryRow(ctx, `SELECT absolute_expires_at FROM auth_sessions WHERE token_hash=$1`, identity.Digest(admin.Value)).Scan(&oldExpiry); e != nil {
		t.Fatal(e)
	}
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	for _, v := range []any{0, 525601, -1, true, 1.5, "1.5", nil} {
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"login-session-duration": v}}, admin)
		expect(t, r, 422)
	}
	changeLoginDuration(t, srv, admin, 2)
	expectedConfig, configErr := a.system.Configuration(ctx, 0)
	if configErr != nil {
		t.Fatal(configErr)
	}
	current := loginAs(t, srv, "duration-user")
	assertCookieDuration(t, current, 120)
	var duration float64
	var version int64
	var phase string
	if e := pool.QueryRow(ctx, `SELECT extract(epoch FROM absolute_expires_at-issued_at),configuration_revision,phase FROM auth_sessions WHERE token_hash=$1`, identity.Digest(current.Value)).Scan(&duration, &version, &phase); e != nil || duration != 120 || version != expectedConfig.ID || phase != "authenticated" {
		t.Fatal("persisted duration", duration, version, phase, e)
	}
	// Cookie creation after another edit must use this session's recorded expiry.
	changeLoginDuration(t, srv, admin, 3)
	recorder := httptest.NewRecorder()
	if e := a.setSession(recorder, httptest.NewRequest("GET", "/", nil), current.Value, true); e != nil {
		t.Fatal(e)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing cookie")
	}
	assertCookieDuration(t, cookies[0], 120)
	if !cookies[0].Secure {
		t.Fatal("secure flag lost")
	}
	var kept time.Time
	if e := pool.QueryRow(ctx, `SELECT absolute_expires_at FROM auth_sessions WHERE token_hash=$1`, identity.Digest(admin.Value)).Scan(&kept); e != nil || !kept.Equal(oldExpiry) {
		t.Fatal("existing expiry changed", e)
	}
	// Completing MFA issues a fresh authenticated session at the current duration.
	secret, _, enrolled := enroll(t, srv, current)
	assertCookieDuration(t, enrolled, 180)
	pending := loginAs(t, srv, "duration-user")
	assertCookieDuration(t, pending, 300)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, pending, origin), 401)
	changeLoginDuration(t, srv, admin, 4)
	verified := call(t, srv.URL, "POST", "/api/validate-totp", map[string]string{"totp": identity.TOTP(secret, time.Now().Unix()/30+1)}, pending, secureOrigin)
	expect(t, verified, 200)
	assertCookieDuration(t, verified.cookie, 240)
	if !verified.cookie.Secure {
		t.Fatal("password MFA upgrade lost HTTPS Secure flag")
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, verified.cookie, origin), 200)
	if _, e := pool.Exec(ctx, `UPDATE auth_sessions SET absolute_expires_at=now()-interval '1 second' WHERE token_hash=$1`, identity.Digest(verified.cookie.Value)); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, verified.cookie, origin), 401)
	if _, e := a.identity.CookieLifetime(ctx, verified.cookie.Value); e == nil {
		t.Fatal("expired session given cookie")
	}
	// Maximum supported duration is converted from minutes to seconds exactly.
	changeLoginDuration(t, srv, admin, 525600)
	var token string
	e := a.db.Within(ctx, func(c context.Context) error {
		state, e := a.system.State(c)
		if e != nil {
			return e
		}
		token, e = a.identity.Issue(c, identity.User{ID: userID, Phase: "authenticated"}, state.AuthEpoch, "127.0.0.1")
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	seconds, e := a.identity.CookieLifetime(ctx, token)
	if e != nil || seconds < 525600*60-5 || seconds > 525600*60 {
		t.Fatal("duration unit or bound", seconds, e)
	}
	// Revision 1 is still the original three-key snapshot.
	history, e := a.system.Configuration(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := history.Values["login-session-duration"]; ok {
		t.Fatal("migration rewrote historical snapshot")
	}
}
