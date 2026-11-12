package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/migrations"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const origin = "http://backend.localhost:5174"
const testPassword = "R01-test-only-strong-password"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("NT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NT_TEST_DATABASE_URL required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	root, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "r01_test_" + identity.NewToken()[:16]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, e = root.Exec(ctx, "CREATE SCHEMA "+quoted); e != nil {
		t.Fatal(e)
	}
	config, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	p, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { p.Close(); _, _ = root.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); root.Close() })
	if e = migrations.Apply(ctx, p); e != nil {
		t.Fatal(e)
	}
	return p
}

type result struct {
	status int
	body   []byte
	cookie *http.Cookie
}

func call(t *testing.T, serverURL, method, path string, body any, cookie *http.Cookie, requestOrigin string) result {
	t.Helper()
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req, e := http.NewRequest(method, serverURL+path, bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", requestOrigin)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var c *http.Cookie
	if len(res.Cookies()) > 0 {
		c = res.Cookies()[0]
	}
	return result{res.StatusCode, b, c}
}
func setupBody() map[string]string {
	return map[string]string{"username": "accept-admin", "password": testPassword, "newPassword2": testPassword, "type": "admin", "recording": "enabled", "watermark": "enabled"}
}
func expect(t *testing.T, r result, status int) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status=%d wanted=%d body=%s", r.status, status, r.body)
	}
}
func TestR01LifecycleAndSecurity(t *testing.T) {
	p := testPool(t)
	a := New(p, Config{AllowedOrigins: []string{origin}, VaultKey: bytes.Repeat([]byte{43}, 32), EncryptionKey: bytes.Repeat([]byte{42}, 32)})
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	expect(t, call(t, srv.URL, "GET", "/api/login-status", nil, nil, origin), 418)
	expect(t, call(t, srv.URL, "POST", "/api/setup-user", setupBody(), nil, "https://evil.example"), 403)
	expect(t, call(t, srv.URL, "POST", "/api/setup-user", setupBody(), nil, origin), 204)
	expect(t, call(t, srv.URL, "POST", "/api/setup-user", setupBody(), nil, origin), 409)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, nil, origin), 401)
	expect(t, call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "accept-admin", "password": "wrong"}, nil, origin), 400)
	login := call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "accept-admin", "password": testPassword, "source": "password", "key": ""}, nil, origin)
	expect(t, login, 200)
	if login.cookie == nil || !login.cookie.HttpOnly || login.cookie.Domain != "" || login.cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("invalid session cookie")
	}
	for _, path := range []string{"/api/account/info", "/api/system/runtime", "/api/system/capabilities", "/api/admin/properties", "/api/system/audit/recent", "/api/license"} {
		r := call(t, srv.URL, "GET", path, nil, login.cookie, origin)
		expect(t, r, 200)
		if strings.Contains(string(r.body), "password_hash") || strings.Contains(string(r.body), testPassword) {
			t.Fatal("secret leaked")
		}
	}
	expect(t, call(t, srv.URL, "GET", "/api/admin/assets", nil, login.cookie, origin), 501)
	expect(t, call(t, srv.URL, "POST", "/api/logout", nil, login.cookie, "https://evil.example"), 403)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, login.cookie, origin), 200)
	// A new process/service instance must accept the same persisted session.
	restarted := httptest.NewServer(New(p, Config{AllowedOrigins: []string{origin}, VaultKey: bytes.Repeat([]byte{43}, 32), EncryptionKey: bytes.Repeat([]byte{42}, 32)}).Handler())
	defer restarted.Close()
	expect(t, call(t, restarted.URL, "GET", "/api/account/info", nil, login.cookie, origin), 200)
	expect(t, call(t, restarted.URL, "POST", "/api/logout", nil, login.cookie, origin), 204)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, login.cookie, origin), 401)
	expect(t, call(t, srv.URL, "POST", "/api/logout", nil, login.cookie, origin), 204)
	var users, ops, logs int
	_ = p.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&users)
	_ = p.QueryRow(context.Background(), `SELECT count(*) FROM operation_logs`).Scan(&ops)
	_ = p.QueryRow(context.Background(), `SELECT count(*) FROM login_logs`).Scan(&logs)
	if users != 1 || ops != 2 || logs != 2 {
		t.Fatalf("unexpected persisted evidence: %d/%d/%d", users, ops, logs)
	}
}
func TestConcurrentSetupAndIdempotentMigration(t *testing.T) {
	p := testPool(t)
	if e := migrations.Apply(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer(New(p, Config{AllowedOrigins: []string{origin}, VaultKey: bytes.Repeat([]byte{43}, 32), EncryptionKey: bytes.Repeat([]byte{42}, 32)}).Handler())
	defer srv.Close()
	statuses := make(chan int, 50)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := call(t, srv.URL, "POST", "/api/setup-user", setupBody(), nil, origin)
			statuses <- r.status
		}()
	}
	wg.Wait()
	close(statuses)
	success, conflict := 0, 0
	for status := range statuses {
		switch status {
		case 204:
			success++
		case 409:
			conflict++
		default:
			t.Errorf("unexpected status %d", status)
		}
	}
	if success != 1 || conflict != 49 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	// Published migration drift must block startup.
	_, e := p.Exec(context.Background(), `UPDATE migration_history SET checksum='changed' WHERE version=1`)
	if e != nil {
		t.Fatal(e)
	}
	if e = migrations.Apply(context.Background(), p); e == nil {
		t.Fatal("checksum drift accepted")
	}
}
func TestRevocationExpiryAndSessionRotation(t *testing.T) {
	p := testPool(t)
	srv := httptest.NewServer(New(p, Config{AllowedOrigins: []string{origin}, VaultKey: bytes.Repeat([]byte{43}, 32), EncryptionKey: bytes.Repeat([]byte{42}, 32)}).Handler())
	defer srv.Close()
	expect(t, call(t, srv.URL, "POST", "/api/setup-user", setupBody(), nil, origin), 204)
	login := func(c *http.Cookie) *http.Cookie {
		r := call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "accept-admin", "password": testPassword}, c, origin)
		expect(t, r, 200)
		return r.cookie
	}
	c1 := login(nil)
	c2 := login(c1)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, c1, origin), 401)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, c2, origin), 200)
	_, e := p.Exec(context.Background(), `UPDATE auth_sessions SET absolute_expires_at=$1`, time.Now().Add(-time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, c2, origin), 401)
	c3 := login(nil)
	_, _ = p.Exec(context.Background(), `UPDATE instance_state SET auth_epoch=auth_epoch+1`)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, c3, origin), 401)
	c4 := login(nil)
	_, e = p.Exec(context.Background(), `UPDATE users SET type='user'`)
	if e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, c4, origin), 200)
	for _, path := range []string{"/api/admin/properties", "/api/system/audit/recent"} {
		expect(t, call(t, srv.URL, "GET", path, nil, c4, origin), 403)
	}
	_, _ = p.Exec(context.Background(), `UPDATE users SET status='disabled'`)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, c4, origin), 401)
}
func TestInvalidPayloadAndRateLimit(t *testing.T) {
	p := testPool(t)
	srv := httptest.NewServer(New(p, Config{AllowedOrigins: []string{origin}, VaultKey: bytes.Repeat([]byte{43}, 32), EncryptionKey: bytes.Repeat([]byte{42}, 32)}).Handler())
	defer srv.Close()
	bad := setupBody()
	bad["password"] = "short"
	bad["newPassword2"] = "short"
	expect(t, call(t, srv.URL, "POST", "/api/setup-user", bad, nil, origin), 422)
	expect(t, call(t, srv.URL, "POST", "/api/setup-user", map[string]string{"unknown": "bad"}, nil, origin), 400)
	expect(t, call(t, srv.URL, "POST", "/api/setup-user", setupBody(), nil, origin), 204)
	for i := 0; i < 21; i++ {
		r := call(t, srv.URL, "POST", "/api/login", map[string]string{"username": fmt.Sprintf("missing-%d", i), "password": "wrong"}, nil, origin)
		want := 400
		if i == 20 {
			want = 429
		}
		expect(t, r, want)
	}
	caps := fmt.Sprint(Capabilities())
	if !strings.Contains(caps, "not_implemented") {
		t.Fatal("unimplemented capability advertised")
	}
}

func TestSetupRollbackWhenAuditFails(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `ALTER TABLE operation_logs RENAME TO operation_logs_unavailable`); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(p, Config{AllowedOrigins: []string{origin}, VaultKey: bytes.Repeat([]byte{43}, 32), EncryptionKey: bytes.Repeat([]byte{42}, 32)}).Handler())
	defer srv.Close()
	expect(t, call(t, srv.URL, "POST", "/api/setup-user", setupBody(), nil, origin), 503)
	var count int
	var initialized bool
	if err := p.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `SELECT initialized_at IS NOT NULL FROM instance_state`).Scan(&initialized); err != nil {
		t.Fatal(err)
	}
	if count != 0 || initialized {
		t.Fatal("partial setup persisted after audit failure")
	}
	if _, err := p.Exec(ctx, `ALTER TABLE operation_logs_unavailable RENAME TO operation_logs`); err != nil {
		t.Fatal(err)
	}
	expect(t, call(t, srv.URL, "POST", "/api/setup-user", setupBody(), nil, origin), 204)
}
