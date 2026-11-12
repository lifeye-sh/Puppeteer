package application

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/identity"
	"os"
	"sync"
	"testing"
	"time"
)

func r02Server(t *testing.T, captcha bool) (*pgxpool.Pool, *App, *httptest.Server) {
	p := testPool(t)
	tlsMode := os.Getenv("NT_TEST_TRANSPORT") == "https"
	a := New(p, Config{AllowedOrigins: []string{origin}, VaultKey: bytes.Repeat([]byte{43}, 32), EncryptionKey: bytes.Repeat([]byte{42}, 32), CaptchaRequired: captcha, SecureCookies: tlsMode})
	var srv *httptest.Server
	if tlsMode {
		srv = httptest.NewTLSServer(a.Handler())
		oldClient, oldDialer := http.DefaultClient, websocket.DefaultDialer
		http.DefaultClient = srv.Client()
		dialer := *oldDialer
		dialer.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
		websocket.DefaultDialer = &dialer
		t.Cleanup(func() { http.DefaultClient = oldClient; websocket.DefaultDialer = oldDialer })
	} else {
		srv = httptest.NewServer(a.Handler())
	}
	t.Cleanup(srv.Close)
	expect(t, call(t, srv.URL, "POST", "/api/setup-user", setupBody(), nil, origin), 204)
	return p, a, srv
}
func object(t *testing.T, r result) map[string]any {
	t.Helper()
	var v map[string]any
	if e := json.Unmarshal(r.body, &v); e != nil {
		t.Fatal(e, string(r.body))
	}
	return v
}
func loginAs(t *testing.T, srv *httptest.Server, name string) *http.Cookie {
	t.Helper()
	r := call(t, srv.URL, "POST", "/api/login", map[string]string{"username": name, "password": testPassword}, nil, origin)
	expect(t, r, 200)
	return r.cookie
}
func postID(t *testing.T, srv *httptest.Server, path string, body any, c *http.Cookie) string {
	r := call(t, srv.URL, "POST", path, body, c, origin)
	expect(t, r, 200)
	return object(t, r)["id"].(string)
}
func userInput(name string, roles, depts []string) map[string]any {
	return map[string]any{"username": name, "nickname": name, "password": testPassword, "type": "user", "status": "enabled", "roleIds": roles, "departmentIds": depts}
}
func TestR02RolesDepartmentsAndRevocation(t *testing.T) {
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	dept := postID(t, srv, "/api/admin/departments", map[string]any{"name": "运维", "weight": 0}, admin)
	child := postID(t, srv, "/api/admin/departments", map[string]any{"name": "二组", "parentId": dept, "weight": 0}, admin)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/departments/"+dept, map[string]any{"name": "运维", "parentId": child, "version": 1}, admin, origin), 422)
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/departments/"+dept, nil, admin, origin), 409)
	role := postID(t, srv, "/api/admin/roles", map[string]any{"name": "operator", "permissions": []string{"user.read", "department.read"}}, admin)
	input := userInput("operator", []string{role}, []string{dept})
	id := postID(t, srv, "/api/admin/users", input, admin)
	op := loginAs(t, srv, "operator")
	expect(t, call(t, srv.URL, "GET", "/api/admin/users/paging", nil, op, origin), 200)
	expect(t, call(t, srv.URL, "GET", "/api/admin/roles", nil, op, origin), 403)
	expect(t, call(t, srv.URL, "POST", "/api/admin/users", userInput("escalation", nil, nil), op, origin), 403)
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/roles/"+role, nil, admin, origin), 409)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/roles/"+role, map[string]any{"name": "operator", "version": 1, "permissions": []string{}}, admin, origin), 200)
	expect(t, call(t, srv.URL, "GET", "/api/admin/users/paging", nil, op, origin), 403)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, op, origin), 200)
	input["version"] = 1
	input["status"] = "disabled"
	input["password"] = ""
	expect(t, call(t, srv.URL, "PUT", "/api/admin/users/"+id, input, admin, origin), 200)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, op, origin), 401)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/users/"+id, input, admin, origin), 409)
	own := object(t, call(t, srv.URL, "GET", "/api/account/info", nil, admin, origin))["id"].(string)
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/users/"+own, nil, admin, origin), 409)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, admin, origin), 200)
}
func TestR02PersistentLockAndUnlock(t *testing.T) {
	p, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("operator", nil, nil), admin)
	for i := 0; i < 5; i++ {
		expect(t, call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "operator", "password": "wrong"}, nil, origin), 400)
	}
	restarted := httptest.NewServer(New(p, Config{AllowedOrigins: []string{origin}, VaultKey: bytes.Repeat([]byte{43}, 32), EncryptionKey: bytes.Repeat([]byte{42}, 32)}).Handler())
	defer restarted.Close()
	expect(t, call(t, restarted.URL, "POST", "/api/login", map[string]string{"username": "operator", "password": testPassword}, nil, origin), 429)
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/login-locked/operator", nil, admin, origin), 204)
	_ = loginAs(t, restarted, "operator")
}
func TestR02CaptchaBindingExpiryAndSingleUse(t *testing.T) {
	p, _, srv := r02Server(t, true)
	ctx := context.Background()
	challenge := call(t, srv.URL, "GET", "/api/captcha", nil, nil, origin)
	expect(t, challenge, 200)
	v := object(t, challenge)
	key := v["key"].(string)
	if _, ok := v["answer"]; ok {
		t.Fatal("captcha answer leaked")
	}
	var x int
	if e := p.QueryRow(ctx, `SELECT answer FROM auth_challenges WHERE id=$1`, key).Scan(&x); e != nil {
		t.Fatal(e)
	}
	payload := map[string]any{"key": key, "purpose": "login", "x": x}
	expect(t, call(t, srv.URL, "POST", "/api/captcha/verify", payload, nil, origin), 400)
	proof := call(t, srv.URL, "POST", "/api/captcha/verify", payload, challenge.cookie, origin)
	expect(t, proof, 200)
	proofKey := object(t, proof)["key"].(string)
	expect(t, call(t, srv.URL, "POST", "/api/captcha/verify", payload, challenge.cookie, origin), 400)
	body := map[string]string{"username": "accept-admin", "password": testPassword, "key": proofKey}
	expect(t, call(t, srv.URL, "POST", "/api/login", body, nil, origin), 400)
	expect(t, call(t, srv.URL, "POST", "/api/login", body, challenge.cookie, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/login", body, challenge.cookie, origin), 400)
	challenge = call(t, srv.URL, "GET", "/api/captcha", nil, nil, origin)
	key = object(t, challenge)["key"].(string)
	_, e := p.Exec(ctx, `UPDATE auth_challenges SET expires_at=now()-interval '1 second' WHERE id=$1`, key)
	if e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/captcha/verify", map[string]any{"key": key, "purpose": "login", "x": 0}, challenge.cookie, origin), 400)
}
func enroll(t *testing.T, srv *httptest.Server, c *http.Cookie) ([]byte, []string, *http.Cookie) {
	t.Helper()
	r := call(t, srv.URL, "POST", "/api/account/mfa/enroll", map[string]string{"password": testPassword}, c, origin)
	expect(t, r, 200)
	secret, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(object(t, r)["secret"].(string))
	if e != nil {
		t.Fatal(e)
	}
	r = call(t, srv.URL, "POST", "/api/account/mfa/confirm", map[string]string{"totp": identity.TOTP(secret, time.Now().Unix()/30)}, c, origin)
	expect(t, r, 200)
	codes := []string{}
	for _, v := range object(t, r)["recoveryCodes"].([]any) {
		codes = append(codes, v.(string))
	}
	return secret, codes, r.cookie
}
func TestR02MFAReplayRecoveryConcurrencyAndReset(t *testing.T) {
	p, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	id := postID(t, srv, "/api/admin/users", userInput("operator", nil, nil), admin)
	op := loginAs(t, srv, "operator")
	secret, codes, current := enroll(t, srv, op)
	var cipher []byte
	if e := p.QueryRow(context.Background(), `SELECT secret_cipher FROM mfa_factors WHERE user_id=$1`, id).Scan(&cipher); e != nil || bytes.Contains(cipher, secret) {
		t.Fatal("MFA secret must be encrypted", e)
	}
	var hashes []string
	if e := p.QueryRow(context.Background(), `SELECT array_agg(code_hash) FROM mfa_recovery_codes WHERE user_id=$1`, id).Scan(&hashes); e != nil || len(hashes) != 10 {
		t.Fatal("recovery code hashes missing", e)
	}
	for _, code := range codes {
		found := false
		for _, hash := range hashes {
			if hash == code {
				t.Fatal("plaintext recovery code")
			}
			if hash == identity.Digest(id+":"+code) {
				found = true
			}
		}
		if !found {
			t.Fatal("recovery digest mismatch")
		}
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, op, origin), 401)
	pending := loginAs(t, srv, "operator")
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, pending, origin), 401)
	expect(t, call(t, srv.URL, "POST", "/api/validate-totp", map[string]string{"totp": identity.TOTP(secret, time.Now().Unix()/30)}, pending, origin), 400)
	valid := call(t, srv.URL, "POST", "/api/validate-totp", map[string]string{"totp": identity.TOTP(secret, time.Now().Unix()/30+1)}, pending, origin)
	expect(t, valid, 200)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, valid.cookie, origin), 200)
	pending1, pending2 := loginAs(t, srv, "operator"), loginAs(t, srv, "operator")
	var wg sync.WaitGroup
	results := make(chan result, 2)
	for _, c := range []*http.Cookie{pending1, pending2} {
		wg.Add(1)
		go func(c *http.Cookie) {
			defer wg.Done()
			results <- call(t, srv.URL, "POST", "/api/validate-totp", map[string]string{"recoveryCode": codes[0]}, c, origin)
		}(c)
	}
	wg.Wait()
	close(results)
	wins := 0
	var recovery *http.Cookie
	for r := range results {
		if r.status == 200 {
			wins++
			recovery = r.cookie
		} else if r.status != 400 && r.status != 401 {
			t.Fatalf("unexpected recovery result %d %s", r.status, r.body)
		}
	}
	if wins != 1 {
		t.Fatal("recovery wins", wins)
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, recovery, origin), 401)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, current, origin), 401)
	var used int
	if e := p.QueryRow(context.Background(), `SELECT count(*) FROM mfa_recovery_codes WHERE user_id=$1 AND used_at IS NOT NULL`, id).Scan(&used); e != nil || used != 1 {
		t.Fatal("recovery persistence", used, e)
	}
	_, _, rebound := enroll(t, srv, recovery)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, rebound, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/admin/users/"+id+"/reset-mfa", map[string]string{"password": testPassword, "reason": "验证管理员恢复流程"}, admin, origin), 204)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, rebound, origin), 401)
	rebind := loginAs(t, srv, "operator")
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, rebind, origin), 401)
	expect(t, call(t, srv.URL, "GET", "/api/account/mfa", nil, rebind, origin), 200)
	if e := p.QueryRow(context.Background(), `SELECT details::text FROM operation_logs WHERE action='mfa.admin_reset' LIMIT 1`).Scan(new(string)); e != nil {
		t.Fatal(e)
	}
}
func TestR02MFAAttemptLimitAndCLIReset(t *testing.T) {
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("operator", nil, nil), admin)
	op := loginAs(t, srv, "operator")
	_, _, _ = enroll(t, srv, op)
	pending := loginAs(t, srv, "operator")
	for i := 0; i < 5; i++ {
		expect(t, call(t, srv.URL, "POST", "/api/validate-totp", map[string]string{"totp": "not-otp"}, pending, origin), 400)
	}
	expect(t, call(t, srv.URL, "POST", "/api/validate-totp", map[string]string{"totp": "000000"}, pending, origin), 401)
	if e := a.ResetMFAFromCLI(context.Background(), "operator", "local-test-operator", "lost test authenticator"); e != nil {
		t.Fatal(e)
	}
	var n int
	if e := p.QueryRow(context.Background(), `SELECT count(*) FROM operation_logs WHERE action='mfa.cli_reset' AND details->>'operator'=$1`, "local-test-operator").Scan(&n); e != nil || n != 1 {
		t.Fatal(fmt.Sprint(n, e))
	}
}

func TestR02FixturesIdempotenceAndMasterKey(t *testing.T) {
	_, a, srv := r02Server(t, false)
	ctx := context.Background()
	fixtures := []FixtureUser{{"operator", testPassword}, {"reviewer", testPassword}, {"denied", testPassword}}
	for i := 0; i < 2; i++ {
		if e := a.SeedR02(ctx, "accept-admin", fixtures); e != nil {
			t.Fatal(e)
		}
	}
	admin := loginAs(t, srv, "accept-admin")
	v := object(t, call(t, srv.URL, "GET", "/api/admin/users/paging", nil, admin, origin))
	if v["total"].(float64) != 4 {
		t.Fatal("fixture duplication", v["total"])
	}
	for _, item := range []struct {
		name, path string
		status     int
	}{
		{"operator", "/api/admin/users/paging", 200}, {"operator", "/api/system/audit/recent", 403},
		{"reviewer", "/api/system/audit/recent", 200}, {"reviewer", "/api/admin/users/paging", 403},
		{"denied", "/api/admin/roles", 403}, {"denied", "/api/account/info", 200},
	} {
		expect(t, call(t, srv.URL, "GET", item.path, nil, loginAs(t, srv, item.name), origin), item.status)
	}
	fixtures[0].Password = "different-strong-password"
	if e := a.SeedR02(ctx, "accept-admin", fixtures); e == nil {
		t.Fatal("must not overwrite fixture credentials")
	}
	if e := a.system.CheckMFAKey(ctx, identity.Digest("key-a")); e != nil {
		t.Fatal(e)
	}
	if e := a.system.CheckMFAKey(ctx, identity.Digest("key-a")); e != nil {
		t.Fatal(e)
	}
	if e := a.system.CheckMFAKey(ctx, identity.Digest("key-b")); e == nil {
		t.Fatal("must reject changed MFA master key")
	}
}

// Read the consumed step rather than the wall clock: enrollment and replay
// assertions may straddle a 30-second TOTP boundary.
func consumedAdminTOTPStep(t *testing.T, a *App) int64 {
	t.Helper()
	var step int64
	if err := a.db.Row(context.Background(), `SELECT f.last_step FROM mfa_factors f JOIN users u ON u.id=f.user_id WHERE u.username='accept-admin'`).Scan(&step); err != nil {
		t.Fatal(err)
	}
	return step
}
func consumedAdminTOTP(t *testing.T, a *App, secret []byte) string {
	t.Helper()
	return identity.TOTP(secret, consumedAdminTOTPStep(t, a))
}
func freshAdminTOTP(t *testing.T, a *App, secret []byte) string {
	t.Helper()
	return identity.TOTP(secret, max(consumedAdminTOTPStep(t, a)+1, time.Now().Unix()/30))
}
