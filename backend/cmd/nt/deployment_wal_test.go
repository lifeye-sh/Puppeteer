package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestWALGuardRequiresReadyEnrolledDatabase(t *testing.T) {
	for _, tc := range []struct {
		name, dsn, body string
		status          int
		allowed         bool
	}{
		{"ready", "postgres://fixture@postgres:5432/puppeteer", `{"status":"ready","frozen":false}`, 200, true},
		{"quarantine", "postgres://fixture@postgres/puppeteer", `{"status":"ready","frozen":true}`, 200, false},
		{"old-server", "postgres://fixture@postgres/puppeteer", `{"status":"ready"}`, 200, false},
		{"database-switched", "postgres://fixture@postgres/nt_restore_fixture", `{"status":"ready","frozen":false}`, 200, false},
		{"external", "postgres://fixture@external/puppeteer", `{"status":"ready","frozen":false}`, 200, false},
		{"unavailable", "postgres://fixture@postgres/puppeteer", `{"status":"ready","frozen":false}`, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/readyz" {
					t.Error(r.URL.Path)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
			t.Setenv("NT_ADDR", ":"+port)
			t.Setenv("NT_RUNTIME_PROFILE", filepath.Join(t.TempDir(), "absent.json"))
			for key, value := range map[string]string{"NT_DATABASE_URL": tc.dsn, "NT_MFA_KEY": strings.Repeat("a", 64), "NT_VAULT_KEY": strings.Repeat("b", 64)} {
				t.Setenv(key, value)
				t.Setenv(key+"_FILE", "")
			}
			if err := deploymentWALGuard(); (err == nil) != tc.allowed {
				t.Fatal(err)
			}
		})
	}
}
