package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/nativeclient"
	"next-terminal/backend/internal/platform/fault"
)

func TestR16RemoteAccessMFA(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	host, port, _ := sshTarget(t)
	asset := r04Asset(t, srv, admin, host, port, true)
	var secret []byte
	// Reset the disposable fixture's consumed step to avoid sleeping between
	// unrelated scenarios. The replay assertion itself never resets it.
	fresh := func() string {
		t.Helper()
		step := time.Now().Unix() / 30
		if _, e := pool.Exec(ctx, `UPDATE mfa_factors SET last_step=$1 WHERE user_id=(SELECT id FROM users WHERE username='accept-admin')`, step-1); e != nil {
			t.Fatal(e)
		}
		return identity.TOTP(secret, step)
	}
	save := func(patch map[string]any, password string, want int) {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		code := ""
		if len(secret) > 0 {
			code = fresh()
		}
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": patch, "password": password, "totp": code}, admin)
		expect(t, r, want)
	}
	status := func() map[string]any {
		t.Helper()
		r := call(t, srv.URL, "GET", "/api/account/access-mfa", nil, admin, origin)
		expect(t, r, 200)
		return object(t, r)
	}
	if status()["required"] != false {
		t.Fatal("default changed")
	}
	save(map[string]any{"access-require-mfa": true}, "", 422)
	save(map[string]any{"access-mfa-expires-at": 1441}, testPassword, 422)
	if e := a.db.Within(ctx, func(c context.Context) error {
		_, err := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{"access-require-mfa": json.RawMessage(`true`)}, "")
		return err
	}); !errors.Is(e, fault.Denied) {
		t.Fatal("domain proof bypass", e)
	}
	for _, node := range []string{"access-a", "access-b"} {
		if _, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	save(map[string]any{"access-require-mfa": true}, testPassword, 202)
	if status()["required"] != true || status()["enrolled"] != false {
		t.Fatal("tightening must be immediate")
	}
	expect(t, call(t, srv.URL, "POST", "/api/account/access-mfa", map[string]string{"totp": "123456"}, admin, origin), 409)
	request := map[string]any{"assetId": asset}
	expect(t, call(t, srv.URL, "POST", "/api/sessions", request, admin, origin), 403)
	secret, _, admin = enroll(t, srv, admin)
	code := fresh()
	expect(t, call(t, srv.URL, "POST", "/api/account/access-mfa", map[string]string{"totp": code}, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/account/access-mfa", map[string]string{"totp": code}, admin, origin), 400)
	for _, mutation := range []string{`UPDATE users SET version=version+1 WHERE username='accept-admin'`, `UPDATE mfa_factors SET secret_cipher=secret_cipher||'\x00'::bytea WHERE user_id=(SELECT id FROM users WHERE username='accept-admin')`} {
		rollback := errors.New("rollback fixture mutation")
		err := a.db.Within(ctx, func(c context.Context) error {
			if _, err := a.db.Exec(c, mutation); err != nil {
				return err
			}
			var user string
			if err := a.db.Row(c, `SELECT id FROM users WHERE username='accept-admin'`).Scan(&user); err != nil {
				return err
			}
			state, err := a.sessions.AccessMFAStatus(c, user, identity.Digest(admin.Value))
			if err != nil {
				return err
			}
			if state.Verified {
				return errors.New("identity or factor change retained proof")
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal(err)
		}
	}
	id, _ := reserve(t, srv, admin, asset, false)
	original, e := a.sessions.Get(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	a.finishSession(id, "closed", "fixture")
	id, _ = reserve(t, srv, admin, asset, false)
	a.finishSession(id, "closed", "fixture")
	if _, e = a.sessions.Reserve(ctx, original, identity.Digest(identity.NewToken()), identity.Digest("another-login")); !errors.Is(e, session.ErrAccessMFA) {
		t.Fatal("cross-login reuse", e)
	}
	if _, e = pool.Exec(ctx, `UPDATE session_access_proofs SET verified_at=clock_timestamp()-interval '6 minutes'`); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/sessions", request, admin, origin), 403)
	save(map[string]any{"access-mfa-expires-at": 0}, testPassword, 202)
	expect(t, call(t, srv.URL, "POST", "/api/account/access-mfa", map[string]string{"totp": fresh()}, admin, origin), 200)
	// A failed reservation rolls proof consumption back with the session INSERT.
	invalid := original
	invalid.AssetID = "00000000-0000-0000-0000-000000000001"
	if _, e = a.sessions.Reserve(ctx, invalid, identity.Digest(identity.NewToken()), identity.Digest(admin.Value)); e == nil {
		t.Fatal("invalid asset reserved")
	}
	if status()["verified"] != true {
		t.Fatal("rollback consumed proof")
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, e := a.sessions.Reserve(ctx, original, identity.Digest(identity.NewToken()), identity.Digest(admin.Value))
			if e == nil {
				a.finishSession(id, "closed", "fixture")
			}
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	success, denied := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, session.ErrAccessMFA) {
			denied++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || denied != 7 || status()["verified"] != false {
		t.Fatal("single-use proof", success, denied)
	}
	// Browser proof is never accepted as an OAuth-family proof. The real native
	// client obtains its own proof and connects through the SSH bridge.
	tlsServer := httptest.NewTLSServer(srv.Config.Handler)
	defer tlsServer.Close()
	if e := a.db.Within(ctx, func(c context.Context) error { return nativeFixtureSetting(c, a, true) }); e != nil {
		t.Fatal(e)
	}
	for _, node := range []string{"access-a", "access-b"} {
		if _, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	access := nativeFixtureToken(t, a, admin, []string{"assets:read", "sessions:connect"})
	for _, browserCookie := range []bool{false, true} {
		req := httptest.NewRequest("POST", "/api/native/v1/access-mfa", strings.NewReader(`{"totp":"123456"}`))
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		if browserCookie {
			req.AddCookie(admin)
		}
		response := httptest.NewRecorder()
		a.Handler().ServeHTTP(response, req)
		if response.Code != 401 {
			t.Fatal("native proof accepted browser context", response.Code)
		}
	}
	client, e := nativeclient.New(tlsServer.URL, tlsServer.Client().Transport)
	if e != nil {
		t.Fatal(e)
	}
	if remote, e := client.ConnectSSH(ctx, nativeclient.Token{Access: access}, asset); e == nil {
		remote.Close()
		t.Fatal("native bypass")
	}
	if e = client.PrepareAccess(ctx, nativeclient.Token{Access: access}, func() (string, error) { return fresh(), nil }); e != nil {
		t.Fatal(e)
	}
	remote, e := client.ConnectSSH(ctx, nativeclient.Token{Access: access}, asset)
	if e != nil {
		t.Fatal(e)
	}
	channel, e := remote.NewSession()
	if e != nil {
		remote.Close()
		t.Fatal(e)
	}
	output, e := channel.CombinedOutput("hostname")
	channel.Close()
	remote.Close()
	if !strings.Contains(string(output), "EXEC RESULT hostname") {
		t.Fatal("native MFA SSH", e, string(output))
	}
	if e = client.PrepareAccess(ctx, nativeclient.Token{Access: access}, nil); e == nil {
		t.Fatal("zero-minute native proof reused")
	}
	// Relaxation remains denied until all live nodes have applied the revision.
	save(map[string]any{"access-require-mfa": false}, testPassword, 202)
	if status()["required"] != true {
		t.Fatal("relaxed before ACK")
	}
	for _, node := range []string{"access-a", "access-b"} {
		if _, e = a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	if status()["required"] != false {
		t.Fatal("relaxation not effective")
	}
	if e = a.identity.QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	var proofs int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM session_access_proofs`).Scan(&proofs); e != nil || proofs != 0 {
		t.Fatal("restore retained proofs", proofs, e)
	}
}

func TestR16AccessConfigurationFreshMFA(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	secret, _, admin := enroll(t, srv, admin)
	v, e := a.system.Configuration(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	save := func(code string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{"access-require-mfa": true}, "password": testPassword, "totp": code}, admin)
		expect(t, r, want)
	}
	save("", 400)
	save(consumedAdminTOTP(t, a, secret), 400)
	save(freshAdminTOTP(t, a, secret), 202)
}
