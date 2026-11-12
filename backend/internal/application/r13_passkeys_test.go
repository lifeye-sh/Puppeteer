package application

import (
	"context"
	"github.com/go-webauthn/webauthn/webauthn"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestR13PasskeyCeremonyConsumption(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	uid := object(t, call(t, srv.URL, "GET", "/api/account/info", nil, admin, origin))["id"].(string)
	ctx := context.Background()
	session := &webauthn.SessionData{Challenge: "independent-random-challenge", Expires: time.Now().Add(time.Minute), RelyingPartyID: "example.com", Origin: "https://example.com"}
	issue := func() string {
		v, e := a.identity.SavePasskeyCeremony(ctx, uid, "register", "browser-binding", session)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	token := issue()
	for _, bad := range []struct{ binding, purpose, user string }{{"wrong", "register", uid}, {"browser-binding", "login", uid}, {"browser-binding", "register", ""}} {
		if _, e := a.identity.ConsumePasskeyCeremony(ctx, token, bad.binding, bad.purpose, bad.user); e == nil {
			t.Fatal("binding bypass")
		}
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := a.identity.ConsumePasskeyCeremony(ctx, token, "browser-binding", "register", uid); e == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("challenge consumed more than once", wins.Load())
	}
	token = issue()
	if _, e := pool.Exec(ctx, `UPDATE passkey_ceremonies SET expires_at=now()-interval '1 second'`); e != nil {
		t.Fatal(e)
	}
	if _, e := a.identity.ConsumePasskeyCeremony(ctx, token, "browser-binding", "register", uid); e == nil {
		t.Fatal("expired challenge")
	}
	token = issue()
	if e := a.identity.QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := a.identity.ConsumePasskeyCeremony(ctx, token, "browser-binding", "register", uid); e == nil {
		t.Fatal("restore retained challenge")
	}
}

func TestR13PasskeyRegistrationConsumesInvalidResponse(t *testing.T) {
	_, a, srv := r02Server(t, false)
	a.config.PasskeyRPID = "example.com"
	a.config.PasskeyOrigins = []string{"https://example.com"}
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, "https://example.com")
	admin := loginAs(t, srv, "accept-admin")
	start := object(t, call(t, srv.URL, "POST", "/api/account/passkeys/start", map[string]string{"password": testPassword}, admin, "https://example.com"))
	body := map[string]any{"token": start["token"], "name": "test", "response": map[string]any{}}
	expect(t, call(t, srv.URL, "POST", "/api/account/passkeys/finish", body, admin, "https://example.com"), 400)
	expect(t, call(t, srv.URL, "POST", "/api/account/passkeys/finish", body, admin, "https://example.com"), 400)
	if len(object(t, call(t, srv.URL, "GET", "/api/account/passkeys", nil, admin, origin))["items"].([]any)) != 0 {
		t.Fatal("invalid registration saved")
	}
}
