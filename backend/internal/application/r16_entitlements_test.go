package application

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/database"
	"next-terminal/backend/internal/modules/entitlements"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/pki"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/modules/website"
	"next-terminal/backend/internal/platform/fault"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func quotaRace(t *testing.T, expected int32, fn func(int) error) {
	t.Helper()
	var wg sync.WaitGroup
	var ok, limited atomic.Int32
	start := make(chan struct{})
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			e := fn(i)
			if e == nil {
				ok.Add(1)
			} else if errors.Is(e, entitlements.ErrQuota) {
				limited.Add(1)
			} else {
				t.Error(e)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if ok.Load() != expected || limited.Load() != 100-expected {
		t.Fatalf("quota race admitted=%d limited=%d expected=%d", ok.Load(), limited.Load(), expected)
	}
}

func TestR16EntitlementCapabilityIntersection(t *testing.T) {
	for _, state := range []string{"valid", "expired", "invalid"} {
		caps := Capabilities()
		caps["protocols"] = (&App{}).protocolCapabilities()
		applyEntitlementCapabilities(caps, entitlements.Snapshot{State: state, Features: map[string]bool{"session.rdp": true, "unknown.future": true}})
		f := caps["features"].(map[string]any)
		for _, key := range []string{"automation.execute", "notification.send", "files.write"} {
			if f[key].(map[string]any)["state"] != "not_entitled" {
				t.Fatal("execution advertised", key)
			}
		}
		for _, key := range []string{"automation.jobs", "notification.delivery", "storage.spaces", "files.sftp", "vault.rotation"} {
			if f[key].(map[string]any)["state"] != "available" {
				t.Fatal("history/recovery workspace unavailable", key)
			}
		}
		if f["session.telnet"].(map[string]any)["state"] == "available" || f["session.ssh"].(map[string]any)["state"] == "available" {
			t.Fatal("denied protocol advertised")
		}
		if f["session.graphics"].(map[string]any)["state"] != "not_implemented" {
			t.Fatal("license enabled unconfigured implementation")
		}
		if _, exists := f["unknown.future"]; exists {
			t.Fatal("invented future capability")
		}
		if caps["protocols"].(map[string]map[string]bool)["rdp"]["connect"] {
			t.Fatal("license bypassed guacd configuration")
		}
		if state == "expired" && f["database.postgres"].(map[string]any)["reason"] != "expired" {
			t.Fatal("expiry not explained")
		}
	}
}

func TestR16EntitlementAtomicQuotas(t *testing.T) {
	p, base, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	host, port, _ := sshTarget(t)
	hostID := r04Asset(t, srv, admin, host, port, true)
	actor, e := base.identity.FindByName(ctx, "accept-admin")
	if e != nil {
		t.Fatal(e)
	}
	hosts, _, e := base.resources.List(ctx, resource.Filter{ID: hostID, Limit: 1, All: true})
	if e != nil || len(hosts) != 1 {
		t.Fatal(e)
	}
	hostAsset := hosts[0]
	ca, key, e := pki.Generate("quota db", []string{"db.internal"}, 2, true, nil)
	clear(key)
	if e != nil {
		t.Fatal(e)
	}
	dbAsset := database.Asset{Name: "quota db", Alias: "quota_db", Host: "db.internal", Port: 3306, Database: "inventory", ReadonlyCredentialID: hostAsset.CredentialID, TLSServerName: "db.internal", TLSCA: string(ca), Enabled: true, ConnectTimeout: 10, Engine: "mysql"}
	dbService := database.New(base.db)
	dbAsset.ID, e = dbService.Save(ctx, dbAsset)
	if e != nil {
		t.Fatal(e)
	}
	dbAsset.Version = 1
	local, e := entitlements.NewLocal([]byte(`{"features":{"identity.users":true,"resource.assets":true,"session.ssh":true,"database.mysql":true,"database.postgres":true},"quotas":{"users":4,"resources":5,"sessions":3}}`))
	if e != nil {
		t.Fatal(e)
	}
	config := base.config
	config.EntitlementLocal = local
	nodes := []*App{New(p, config), New(p, config)}
	// Two independently constructed application nodes must share the same locks.
	quotaRace(t, 3, func(i int) error {
		_, e := nodes[i%2].identity.SaveUser(ctx, identity.ManagedUser{Username: fmt.Sprintf("quota-user-%d", i), Nickname: "quota", Type: "user", Status: "disabled"}, "!disabled-fixture")
		return e
	})
	quotaRace(t, 3, func(i int) error {
		a := nodes[i%2]
		switch i % 3 {
		case 0:
			v := hostAsset
			v.ID = ""
			v.Name = fmt.Sprintf("quota host %d", i)
			v.Alias = fmt.Sprintf("quota_host_%d", i)
			_, e := a.resources.Save(ctx, v)
			return e
		case 1:
			v := dbAsset
			v.ID = ""
			v.Name = fmt.Sprintf("quota db %d", i)
			v.Alias = fmt.Sprintf("quota_db_%d", i)
			_, e := database.New(a.db).Save(ctx, v)
			return e
		default:
			_, e := website.New(a.db).Save(ctx, website.Website{Name: fmt.Sprintf("quota web %d", i), Domain: fmt.Sprintf("quota%d.internal", i), Upstream: "http://127.0.0.1:8080", EntryPath: "/", Mode: "private", Enabled: true, TempTTL: 60, TempMax: 120}, "")
			return e
		}
	})
	// Edits and grouping are not additional resources, even at the limit.
	if _, e = nodes[0].resources.Save(ctx, hostAsset); e != nil {
		t.Fatal("edit blocked at capacity", e)
	}
	quotaRace(t, 3, func(i int) error {
		a := nodes[i%2]
		if i%3 == 0 {
			_, e := a.sessions.Reserve(ctx, session.Session{ActorID: actor.ID, AssetID: hostID, AssetVersion: 1, CredentialVersion: 1, HostKeyVersion: 1, Protocol: "ssh"}, identity.NewToken(), identity.NewToken())
			return e
		}
		v := dbAsset
		if i%3 == 2 {
			v.Engine = "postgresql"
		}
		_, e := database.New(a.db).BeginSession(ctx, actor.ID, v, 1, "fixture")
		return e
	})
	status, e := nodes[0].entitlements.Status(ctx)
	if e != nil || status.Used["users"] != 4 || status.Used["resources"] != 5 || status.Used["sessions"] != 3 {
		t.Fatal(status, e)
	}
	// Expired reservations do not permanently leak capacity after a crash.
	if _, e = p.Exec(ctx, `UPDATE sessions SET lease_expires_at=now()-interval '1 second'; UPDATE database_sessions SET quota_lease_until=now()-interval '1 second'`); e != nil {
		t.Fatal(e)
	}
	service := database.New(nodes[0].db)
	id, e := service.BeginSession(ctx, actor.ID, dbAsset, 1, "replacement")
	if e != nil {
		t.Fatal(e)
	}
	if e = service.RenewSession(ctx, id, "mysql"); e != nil {
		t.Fatal(e)
	}
	queryID, e := service.BeginQuery(ctx, id, "SELECT 1", 3, []any{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE instance_state SET auth_epoch=auth_epoch+1`); e != nil {
		t.Fatal(e)
	}
	if e = service.RenewSession(ctx, id, "mysql"); !errors.Is(e, fault.Denied) {
		t.Fatal("old epoch renewed", e)
	}
	if e = service.ReapSessions(ctx); e != nil {
		t.Fatal(e)
	}
	var closed bool
	var queryState string
	if e = p.QueryRow(ctx, `SELECT ended_at IS NOT NULL FROM database_sessions WHERE id=$1`, id).Scan(&closed); e != nil || !closed {
		t.Fatal("expired audit not finalized", e)
	}
	if e = p.QueryRow(ctx, `SELECT state FROM database_sql_logs WHERE id=$1`, queryID).Scan(&queryState); e != nil || queryState != "unknown" {
		t.Fatal(queryState, e)
	}
	status, e = nodes[1].entitlements.Status(ctx)
	if e != nil || status.Used["sessions"] != 0 {
		t.Fatal(status, e)
	}
	// Rolled-back reservations never consume a slot.
	errRollback := errors.New("fixture rollback")
	e = nodes[0].db.Within(ctx, func(c context.Context) error {
		if _, e := service.BeginSession(c, actor.ID, dbAsset, 1, "rollback"); e != nil {
			return e
		}
		return errRollback
	})
	if !errors.Is(e, errRollback) {
		t.Fatal(e)
	}
	status, e = nodes[0].entitlements.Status(ctx)
	if e != nil || status.Used["sessions"] != 0 {
		t.Fatal(status, e)
	}

	// Hold a renewal uncommitted until the OLD lease has expired. Another node
	// must wait for that renewal, then observe its new expiry and reject admission.
	one, e := entitlements.NewLocal([]byte(`{"features":{"database.mysql":true},"quotas":{"users":null,"resources":null,"sessions":1}}`))
	if e != nil {
		t.Fatal(e)
	}
	config.EntitlementLocal = one
	first, second := New(p, config), New(p, config)
	leaseService := database.New(first.db)
	liveID, e := leaseService.BeginSession(ctx, actor.ID, dbAsset, 1, "renewal-race")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE database_sessions SET quota_lease_until=clock_timestamp()+interval '2 seconds' WHERE id=$1`, liveID); e != nil {
		t.Fatal(e)
	}
	ready, release := make(chan struct{}), make(chan struct{})
	renewed := make(chan error, 1)
	admitted := make(chan error, 1)
	go func() {
		renewed <- first.db.Within(ctx, func(c context.Context) error {
			err := leaseService.RenewSession(c, liveID, "mysql")
			close(ready)
			if err != nil {
				return err
			}
			<-release
			return nil
		})
	}()
	<-ready
	go func() {
		_, err := database.New(second.db).BeginSession(ctx, actor.ID, dbAsset, 1, "must-wait")
		admitted <- err
	}()
	var premature bool
	var admissionErr error
	select {
	case admissionErr = <-admitted:
		premature = true
	case <-time.After(2200 * time.Millisecond):
	}
	close(release)
	if e = <-renewed; e != nil {
		t.Fatal(e)
	}
	if premature {
		t.Fatal("admission bypassed pending renewal", admissionErr)
	}
	if e = <-admitted; !errors.Is(e, entitlements.ErrQuota) {
		t.Fatal("renewal overbooked", e)
	}
}

func TestR16ZeroQuotaPreservesRecoveryAndEdits(t *testing.T) {
	p, base, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	local, e := entitlements.NewLocal([]byte(`{"features":{"identity.users":true,"resource.assets":true,"session.ssh":true},"quotas":{"users":0,"resources":0,"sessions":0}}`))
	if e != nil {
		t.Fatal(e)
	}
	config := base.config
	config.EntitlementLocal = local
	a := New(p, config)
	limited := httptest.NewServer(a.Handler())
	defer limited.Close()
	r := call(t, limited.URL, "POST", "/api/admin/users", userInput("zero-limit", nil, nil), admin, origin)
	expect(t, r, 409)
	if object(t, r)["reason"] != "quota_exceeded" {
		t.Fatal(string(r.body))
	}
	expect(t, call(t, limited.URL, "GET", "/api/admin/entitlements", nil, admin, origin), 200)
	expect(t, call(t, limited.URL, "GET", "/api/admin/users/paging", nil, admin, origin), 200)
	expect(t, call(t, limited.URL, "GET", "/api/account/info", nil, admin, origin), 200)
	if e = a.db.Within(ctx, func(c context.Context) error { return a.db.Admit(c, "resources", "resource.assets") }); !errors.Is(e, entitlements.ErrQuota) {
		t.Fatal(e)
	}
	if _, e = a.identity.CreateAdmin(ctx, "extra-admin", "fixture", "!disabled-fixture"); !errors.Is(e, entitlements.ErrQuota) {
		t.Fatal("extra admin bypass", e)
	}
	if e = a.db.Admit(ctx, "sessions", "session.ssh"); e == nil {
		t.Fatal("admission outside transaction accepted")
	}
	// Even a local-mode administrator cannot install a key or token through HTTP.
	expect(t, call(t, limited.URL, "POST", "/api/admin/entitlements", map[string]any{"password": testPassword, "token": "irrelevant", "revision": 0}, admin, origin), 403)
}

func TestR16SignedEntitlementImportAndExpiry(t *testing.T) {
	p, base, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("license-member", nil, nil), admin)
	member := loginAs(t, srv, "license-member")
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	verifier, e := entitlements.NewVerifier(map[string]ed25519.PublicKey{"fixture": pub})
	if e != nil {
		t.Fatal(e)
	}
	config := base.config
	config.EntitlementVerifier = verifier
	a := New(p, config)
	signed := httptest.NewServer(a.Handler())
	defer signed.Close()
	state, e := a.system.State(ctx)
	if e != nil {
		t.Fatal(e)
	}
	makeToken := func(instance string, expiry time.Time, features map[string]bool) string {
		c := entitlements.Claims{Issuer: entitlements.Issuer, Audience: entitlements.Audience, Product: entitlements.Product, InstanceID: instance, LicenseID: "be369494-6463-4c58-884c-66c084fc4de4", IssuedAt: time.Now().Add(-time.Hour).Unix(), NotBefore: time.Now().Add(-time.Hour).Unix(), ExpiresAt: expiry.Unix(), SchemaVersion: 1, Features: features}
		raw, _ := json.Marshal(c)
		body := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA","kid":"fixture","typ":"nt-license+jws"}`)) + "." + base64.RawURLEncoding.EncodeToString(raw)
		return body + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(body)))
	}
	valid := makeToken(state.InstanceID, time.Now().Add(time.Hour), map[string]bool{"identity.users": true})
	expect(t, call(t, signed.URL, "GET", "/api/admin/entitlements", nil, member, origin), 403)
	body := map[string]any{"token": valid, "revision": 0, "password": testPassword}
	expect(t, call(t, signed.URL, "POST", "/api/admin/entitlements", body, member, origin), 403)
	body["password"] = "wrong"
	expect(t, call(t, signed.URL, "POST", "/api/admin/entitlements", body, admin, origin), 400)
	body["password"] = testPassword
	body["token"] = makeToken("b93001aa-ae70-4e4e-a1dd-c5e16db37440", time.Now().Add(time.Hour), map[string]bool{})
	expect(t, call(t, signed.URL, "POST", "/api/admin/entitlements", body, admin, origin), 422)
	body["token"] = valid
	r := call(t, signed.URL, "POST", "/api/admin/entitlements", body, admin, origin)
	expect(t, r, 200)
	rev := object(t, r)["revision"]
	expect(t, call(t, signed.URL, "POST", "/api/admin/entitlements", body, admin, origin), 409)
	body["revision"] = rev
	expect(t, call(t, signed.URL, "POST", "/api/admin/entitlements", body, admin, origin), 200)
	expect(t, call(t, signed.URL, "POST", "/api/admin/users", userInput("licensed-user", nil, nil), admin, origin), 200)
	if e = a.db.Within(ctx, func(c context.Context) error { return a.db.Admit(c, "sessions", "session.ssh") }); !errors.Is(e, entitlements.ErrDenied) {
		t.Fatal(e)
	}
	response := object(t, call(t, signed.URL, "GET", "/api/system/capabilities", nil, admin, origin))
	if response["features"].(map[string]any)["session.ssh"].(map[string]any)["state"] == "available" {
		t.Fatal("capability ignored entitlement")
	}
	expired := makeToken(state.InstanceID, time.Now().Add(-2*time.Minute), map[string]bool{"identity.users": true})
	body["token"] = expired
	expect(t, call(t, signed.URL, "POST", "/api/admin/entitlements", body, admin, origin), 403)
	// Simulate passage of time without sleeping or changing the machine clock.
	if _, e = p.Exec(ctx, `UPDATE entitlement_licenses SET token=$1 WHERE revision=$2`, expired, rev); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, signed.URL, "POST", "/api/admin/users", userInput("expired-user", nil, nil), admin, origin), 403)
	expect(t, call(t, signed.URL, "GET", "/api/account/info", nil, admin, origin), 200)
	status, e := a.entitlements.Status(ctx)
	if e != nil || status.State != "expired" {
		t.Fatal(status, e)
	}
	// The audit payload cannot disclose the license token or proof password.
	var leaked bool
	if e = p.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM operation_logs WHERE action='system.license_imported' AND (details::text LIKE $1 OR details::text LIKE $2))`, "%"+valid+"%", "%"+testPassword+"%").Scan(&leaked); e != nil || leaked {
		t.Fatal("license audit leak", e)
	}
}
