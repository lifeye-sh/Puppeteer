package application

import (
	"context"
	"errors"
	"next-terminal/backend/internal/modules/identity"
	"sync"
	"sync/atomic"
	"testing"
)

func TestR16LoginSessionCapacity(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	uid := postID(t, srv, "/api/admin/users", userInput("session-capacity", nil, nil), admin)
	settings := func(enabled bool, limit int) {
		t.Helper()
		_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"login-session-count-custom": enabled, "login-session-count-limit": limit}}, admin)
		expect(t, r, 202)
	}
	settings(true, 3)
	state, err := a.system.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	user := identity.User{ID: uid, Username: "session-capacity", Type: "user", Phase: "authenticated"}
	var wg sync.WaitGroup
	var issued, limited atomic.Int32
	tokens := make(chan string, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, e := a.identity.Issue(ctx, user, state.AuthEpoch, "127.0.0.1")
			if e == nil {
				issued.Add(1)
				tokens <- token
			} else if errors.Is(e, identity.ErrSessionLimit) {
				limited.Add(1)
			} else {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	close(tokens)
	if issued.Load() != 3 || limited.Load() != 97 {
		t.Fatal("capacity race", issued.Load(), limited.Load())
	}
	saved := []string{}
	for token := range tokens {
		saved = append(saved, token)
	}
	// HTTP authentication uses the same gate; error is actionable and not a 503.
	r := call(t, srv.URL, "POST", "/api/login", map[string]any{"username": "session-capacity", "password": testPassword}, nil, origin)
	expect(t, r, 409)
	if object(t, r)["reason"] != "login_session_limit" {
		t.Fatal(string(r.body))
	}
	// MFA pending sessions cannot exhaust the authenticated quota.
	pending := user
	pending.Phase = "mfa_pending"
	if _, err = a.identity.Issue(ctx, pending, state.AuthEpoch, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err = a.identity.Revoke(ctx, saved[0]); err != nil {
		t.Fatal(err)
	}
	replacement, err := a.identity.Issue(ctx, user, state.AuthEpoch, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE auth_sessions SET absolute_expires_at=now()-interval '1 second' WHERE token_hash=$1`, identity.Digest(saved[1])); err != nil {
		t.Fatal(err)
	}
	if _, err = a.identity.Issue(ctx, user, state.AuthEpoch, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	settings(true, 1)
	if current, e := a.identity.Current(ctx, replacement); e != nil || current == nil {
		t.Fatal("lower cap revoked existing session", e)
	}
	if _, err = a.identity.Issue(ctx, user, state.AuthEpoch, "127.0.0.1"); !errors.Is(err, identity.ErrSessionLimit) {
		t.Fatal(err)
	}
	settings(false, 1)
	if _, err = a.identity.Issue(ctx, user, state.AuthEpoch, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	for _, value := range []any{0, 1001, nil, true, 1.5} {
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"login-session-count-limit": value}}, admin)
		expect(t, r, 422)
	}
	// Old auth epochs no longer consume capacity; a newly issued login still works.
	if _, err = pool.Exec(ctx, `UPDATE auth_sessions SET auth_epoch=auth_epoch-1 WHERE user_id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	settings(true, 1)
	if _, err = a.identity.Issue(ctx, user, state.AuthEpoch, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
}
