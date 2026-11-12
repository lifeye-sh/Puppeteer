package application

import (
	"context"
	"encoding/json"
	"errors"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"testing"
)

func TestR16CaptchaConfigurationBarrierProofAndRestore(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	a.config.CaptchaRequired = true
	ctx := context.Background()
	apply := func(node string) {
		t.Helper()
		if _, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	apply("captcha-a")
	apply("captcha-b")
	status := func(want bool) {
		t.Helper()
		v := object(t, call(t, srv.URL, "GET", "/api/login-status", nil, nil, origin))
		if v["captchaEnabled"] != want {
			t.Fatal("incorrect effective CAPTCHA policy", v["captchaEnabled"])
		}
	}
	save := func(value bool, password string, want int) {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{system.LoginCaptchaKey: value}, "password": password}, admin)
		expect(t, r, want)
	}
	login := func(want int) {
		t.Helper()
		expect(t, call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "accept-admin", "password": testPassword}, nil, origin), want)
	}
	seed := func() {
		t.Helper()
		if _, e := pool.Exec(ctx, `INSERT INTO auth_challenges(id,purpose,binding_hash,answer,verified,expires_at) VALUES('r16-captcha-policy-proof','login-proof','isolated',0,true,now()+interval '2 minutes')`); e != nil {
			t.Fatal(e)
		}
	}
	count := func(want int) {
		t.Helper()
		var n int
		if e := pool.QueryRow(ctx, `SELECT count(*) FROM auth_challenges WHERE id='r16-captcha-policy-proof'`).Scan(&n); e != nil || n != want {
			t.Fatal(n, e)
		}
	}
	status(true)
	login(400)
	if e := a.db.Within(ctx, func(c context.Context) error {
		_, e := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{system.LoginCaptchaKey: json.RawMessage("false")}, "")
		return e
	}); !errors.Is(e, fault.Denied) {
		t.Fatal("domain write bypassed proof", e)
	}
	save(false, "", 422)
	save(false, "incorrect-isolated-password", 400)
	seed()
	save(false, testPassword, 202)
	count(0)
	status(true)
	login(400)
	apply("captcha-a")
	status(true)
	apply("captcha-b")
	status(false)
	login(200)
	seed()
	save(false, testPassword, 202)
	count(1) // No-op saves do not invalidate proofs.
	save(true, testPassword, 202)
	count(0)
	status(true)
	login(400)
	apply("captcha-a")
	apply("captcha-b")
	save(false, testPassword, 202)
	apply("captcha-a")
	apply("captcha-b")
	status(false)
	if e := a.identity.QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	status(true)
}

func TestR16CaptchaConfigurationFreshMFA(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	secret, _, admin := enroll(t, srv, admin)
	v, e := a.system.Configuration(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	save := func(code string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{system.LoginCaptchaKey: false}, "password": testPassword, "totp": code}, admin)
		expect(t, r, want)
	}
	save("", 400)
	save(consumedAdminTOTP(t, a, secret), 400)
	save(freshAdminTOTP(t, a, secret), 202)
}
