package application

import (
	"context"
	"encoding/json"
	"errors"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"testing"
	"time"
)

func TestR16OAuthLifetimeProofBoundsAndNativeCap(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	current := func() system.Revision {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	save := func(set map[string]any, password string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(current().ID), map[string]any{"set": set, "password": password}, admin)
		expect(t, r, want)
	}
	original := current()
	if original.Values.Int("oidc-server-refresh-token-ttl") != 2592000 {
		t.Fatal("compatibility default")
	}
	save(map[string]any{"oidc-server-access-token-ttl": 120}, "", 422)
	for _, bad := range []map[string]any{
		{"oidc-server-access-token-ttl": 59}, {"oidc-server-access-token-ttl": 3601},
		{"oidc-server-refresh-token-ttl": 3599}, {"oidc-server-refresh-token-ttl": 2592001},
		{"oidc-server-id-token-ttl": 59}, {"oidc-server-id-token-ttl": 3601},
		{"oidc-server-auth-code-ttl": 29}, {"oidc-server-auth-code-ttl": 301},
	} {
		save(bad, testPassword, 422)
	}
	if current().ID != original.ID {
		t.Fatal("invalid save committed")
	}
	e := a.db.Within(ctx, func(c context.Context) error {
		_, e := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{"oidc-server-access-token-ttl": json.RawMessage("120")}, "")
		return e
	})
	if !errors.Is(e, fault.Denied) {
		t.Fatal("direct service bypass", e)
	}
	postID(t, srv, "/api/admin/users", userInput("oauth-lifetime-member", nil, nil), admin)
	member := loginAs(t, srv, "oauth-lifetime-member")
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(current().ID), map[string]any{"set": map[string]any{"oidc-server-access-token-ttl": 120}, "password": testPassword}, member)
	expect(t, r, 403)
	save(map[string]any{"oidc-server-access-token-ttl": 3600, "oidc-server-refresh-token-ttl": 2592000, "oidc-server-id-token-ttl": 3600, "oidc-server-auth-code-ttl": 300}, testPassword, 202)
	token := nativeFixtureToken(t, a, admin, []string{"assets:read", "offline_access"})
	var family string
	var accessRemaining, familyRemaining float64
	if e = pool.QueryRow(ctx, `SELECT f.id,extract(epoch FROM(t.expires_at-now())),extract(epoch FROM(f.expires_at-now())) FROM oauth_tokens t JOIN oauth_token_families f ON f.id=t.family_id WHERE t.token_hash=$1`, identity.Digest(token)).Scan(&family, &accessRemaining, &familyRemaining); e != nil {
		t.Fatal(e)
	}
	if accessRemaining > 300 || accessRemaining < 280 || familyRemaining > 604800 || familyRemaining < 604780 {
		t.Fatal("native maxima", accessRemaining, familyRemaining)
	}
	// New issuance uses new values, but neither existing access nor family deadlines move.
	var oldAccess, oldFamily time.Time
	if e = pool.QueryRow(ctx, `SELECT t.expires_at,f.expires_at FROM oauth_tokens t JOIN oauth_token_families f ON f.id=t.family_id WHERE t.token_hash=$1`, identity.Digest(token)).Scan(&oldAccess, &oldFamily); e != nil {
		t.Fatal(e)
	}
	save(map[string]any{"oidc-server-access-token-ttl": 60, "oidc-server-refresh-token-ttl": 3600}, testPassword, 202)
	var nextAccess, nextRefresh string
	var expires int
	e = a.db.Within(ctx, func(c context.Context) error {
		g, _, e := a.identity.OAuthToken(c, token, "access", identity.NativeClientUUID, false)
		if e != nil {
			return e
		}
		nextAccess, nextRefresh, expires, e = a.identity.IssueOAuthTokens(c, g, true)
		return e
	})
	if e != nil || expires != 60 {
		t.Fatal("new issuance", expires, e)
	}
	var actualAccess, actualFamily, refreshDeadline time.Time
	if e = pool.QueryRow(ctx, `SELECT t.expires_at,f.expires_at FROM oauth_tokens t JOIN oauth_token_families f ON f.id=t.family_id WHERE t.token_hash=$1`, identity.Digest(token)).Scan(&actualAccess, &actualFamily); e != nil {
		t.Fatal(e)
	}
	if e = pool.QueryRow(ctx, `SELECT expires_at FROM oauth_tokens WHERE token_hash=$1`, identity.Digest(nextRefresh)).Scan(&refreshDeadline); e != nil {
		t.Fatal(e)
	}
	if !actualAccess.Equal(oldAccess) || !actualFamily.Equal(oldFamily) || !refreshDeadline.Equal(oldFamily) {
		t.Fatal("existing deadline changed")
	}
	// A nearly expired family caps access expiry and the returned expires_in.
	if _, e = pool.Exec(ctx, `UPDATE oauth_token_families SET expires_at=now()+interval '20 seconds' WHERE id=$1`, family); e != nil {
		t.Fatal(e)
	}
	e = a.db.Within(ctx, func(c context.Context) error {
		g, _, e := a.identity.OAuthToken(c, nextAccess, "access", identity.NativeClientUUID, false)
		if e != nil {
			return e
		}
		nextAccess, _, expires, e = a.identity.IssueOAuthTokens(c, g, false)
		return e
	})
	if e != nil || expires > 20 || expires < 15 {
		t.Fatal("family cap", expires, e)
	}
	var capped bool
	if e = pool.QueryRow(ctx, `SELECT t.expires_at=f.expires_at FROM oauth_tokens t JOIN oauth_token_families f ON f.id=t.family_id WHERE t.token_hash=$1`, identity.Digest(nextAccess)).Scan(&capped); e != nil || !capped {
		t.Fatal("expiry exceeds family", e)
	}
}

func TestR16OAuthLifetimeFreshMFA(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	secret, _, admin := enroll(t, srv, admin)
	v, e := a.system.Configuration(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	save := func(code string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{"oidc-server-id-token-ttl": 120}, "password": testPassword, "totp": code}, admin)
		expect(t, r, want)
	}
	save("", 400)
	save(consumedAdminTOTP(t, a, secret), 400)
	save(freshAdminTOTP(t, a, secret), 202)
}
