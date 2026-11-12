package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// Explicitly opt-in isolated fixture; never points at the deployment's business schema.
func TestR17BrowserFixture(t *testing.T) {
	if os.Getenv("NT_R17_BROWSER_FIXTURE") != "1" {
		t.Skip("browser fixture disabled")
	}
	p := testPool(t)
	a := New(p, Config{AllowedOrigins: []string{origin, "https://localhost:8443"}, VaultKey: bytes.Repeat([]byte{43}, 32), EncryptionKey: bytes.Repeat([]byte{42}, 32), SecureCookies: true, CaptchaRequired: false})
	local := httptest.NewServer(a.Handler())
	defer local.Close()
	expect(t, call(t, local.URL, "POST", "/api/setup-user", setupBody(), nil, origin), 204)
	admin := loginAs(t, local, "accept-admin")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if proxy := os.Getenv("NT_R17_PROXY_IP"); proxy != "" {
		var actor string
		if e := p.QueryRow(ctx, `SELECT id FROM users WHERE username='accept-admin'`).Scan(&actor); e != nil {
			t.Fatal(e)
		}
		ip, _ := json.Marshal(proxy + "/32")
		if e := a.db.Within(ctx, func(c context.Context) error {
			_, e := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{"ip-extractor": json.RawMessage(`"x-forwarded-for"`), "ip-trust-list": ip}, actor)
			return e
		}); e != nil {
			t.Fatal(e)
		}
	}
	a.config.MP4URL = "http://renderer:8090"
	a.config.MP4Token = os.Getenv("NT_MP4_TEST_TOKEN")
	a.config.MP4Directory = t.TempDir()
	go a.RunRecordingExports(ctx)
	fileRoot := t.TempDir()
	host, port, _ := sshTargetRoot(t, fileRoot)
	r04Asset(t, local, admin, host, port, true)
	handler := a.Handler()
	server := &http.Server{Addr: ":8089", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/__fixture/audit" {
			var n int
			_ = p.QueryRow(r.Context(), `SELECT count(*) FROM sessions WHERE client_ip='127.0.0.1' AND closed_at IS NOT NULL`).Scan(&n)
			_ = json.NewEncoder(w).Encode(map[string]any{"closedSessionsWithClientIP": n, "fileRoot": fileRoot})
			return
		}
		handler.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 10 * time.Second}
	defer server.Close()
	go func() { _ = server.ListenAndServe() }()
	t.Log("R17 isolated browser fixture ready; expires in ten minutes")
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Minute):
	}
}
