package application

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Opt-in desktop fixture. A separate disposable schema is held for at most
// 30 minutes by default (explicitly bounded to 4 hours); it never changes a business account, asset, or password.
func TestR16DBeaverManualFixture(t *testing.T) {
	dir := os.Getenv("NT_R16_DBEAVER_DIR")
	target := os.Getenv("NT_R16_PG_FIXTURE")
	if dir == "" || target == "" {
		t.Skip("explicit manual desktop fixture only")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("absolute fixture directory required")
	}
	dsn, err := url.Parse(os.Getenv("NT_TEST_DATABASE_URL"))
	if err != nil || dsn.Hostname() != "r16-postgres-fixture" || dsn.Path != "/nt_r16_test" {
		t.Fatal("refuse non-disposable desktop database")
	}
	minutes := 30
	if raw := os.Getenv("NT_R16_DBEAVER_MINUTES"); raw != "" {
		minutes, err = strconv.Atoi(raw)
		if err != nil || minutes < 5 || minutes > 240 {
			t.Fatal("desktop fixture lifetime must be 5 to 240 minutes")
		}
	}
	ttl := time.Duration(minutes) * time.Minute
	marker := "R16_DBEAVER_" + strconv.FormatInt(time.Now().UnixNano(), 16)
	acceptanceQuery := "SELECT id, label FROM items WHERE id = 1 /* " + marker + " */;"
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	user := postID(t, srv, "/api/admin/users", userInput("pg-desktop", nil, nil), admin)
	reader := loginAs(t, srv, "pg-desktop")
	ca, e := os.ReadFile(filepath.Join(target, "server.pem"))
	if e != nil {
		t.Fatal(e)
	}
	cred := postID(t, srv, "/api/admin/credentials", map[string]any{"name": "Desktop fixture credential", "kind": "password", "username": "reader", "secret": map[string]string{"password": "r16-fixture-reader"}}, admin)
	asset := postID(t, srv, "/api/admin/database-assets", map[string]any{"name": "Desktop PG fixture", "alias": "desktop", "engine": "postgresql", "host": "r16-postgres-fixture", "port": 5432, "database": "inventory", "readonlyCredentialId": cred, "tlsServerName": "r16-postgres-fixture", "tlsCa": string(ca), "enabled": true, "connectTimeout": 5}, admin)
	postID(t, srv, "/api/admin/grants", map[string]any{"userId": user, "resourceId": asset}, admin)
	password := object(t, call(t, srv.URL, "POST", "/api/account/database-password", map[string]any{"password": testPassword, "days": 1}, reader, origin))["password"].(string)
	a.config.DatabaseProxyName = "localhost"
	ctx, cancel := context.WithTimeout(context.Background(), ttl)
	defer cancel()
	config, e := a.databaseProxyTLS(ctx)
	if e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "0.0.0.0:15433")
	if e != nil {
		t.Fatal(e)
	}
	protocol := a.postgresProtocol(config)
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			workers.Add(1)
			go func() { defer workers.Done(); protocol.ServeConn(ctx, c) }()
		}
	}()
	defer func() { listener.Close(); <-done; cancel(); workers.Wait() }()
	if e = os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	// Only these fixture-owned files are removed; prior workspace content is untouched.
	for _, name := range []string{"connection.json", "complete"} {
		if e = os.Remove(filepath.Join(dir, name)); e != nil && !os.IsNotExist(e) {
			t.Fatal(e)
		}
	}
	data, _ := json.MarshalIndent(map[string]any{"host": "localhost", "port": 15433, "database": "inventory", "username": "pg-desktop@desktop", "password": password, "sslmode": "verify-full", "sslrootcert": "proxy-ca.pem", "readOnlyMode": "always", "acceptanceQuery": acceptanceQuery, "expiresAt": time.Now().Add(ttl).UTC()}, "", "  ")
	if e = os.WriteFile(filepath.Join(dir, "proxy-ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: config.Certificates[0].Certificate[0]}), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "connection.json"), data, 0600); e != nil {
		t.Fatal(e)
	}
	defer os.Remove(filepath.Join(dir, "connection.json"))
	t.Log("isolated DBeaver fixture ready; credentials are in the local connection file, never in logs")
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	warned := false
	for {
		select {
		case <-ctx.Done():
			t.Fatal("manual desktop acceptance not confirmed within the configured lifetime")
		case <-tick.C:
			if _, e = os.Stat(filepath.Join(dir, "complete")); e == nil {
				var completed int
				if e = pool.QueryRow(ctx, `SELECT count(*) FROM database_sql_logs WHERE state='completed' AND command IN (69,81) AND row_count=1 AND sql_text LIKE $1`, "%"+marker+"%").Scan(&completed); e != nil {
					t.Fatal("cannot verify desktop query audit", e)
				}
				if completed < 1 {
					if !warned {
						t.Log("desktop confirmation received but this run's marked query is missing; keeping fixture available until its configured expiry")
						warned = true
					}
					continue
				}
				// This verifies the actual query reached the proxy. Desktop rendering and
				// object-tree correctness still require the user's explicit observation.
				t.Logf("confirmed desktop run has %d completed marked queries", completed)
				return
			} else if !os.IsNotExist(e) {
				t.Fatal(e)
			}
		}
	}
}
