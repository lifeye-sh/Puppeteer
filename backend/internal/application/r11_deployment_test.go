package application

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/backup"
	"next-terminal/backend/internal/modules/storage"
	"next-terminal/backend/internal/platform/runtimeprofile"
	"os"
	"strings"
	"testing"
)

// Opt-in verification for the disposable Compose drill, never the live database.
func TestR11DeploymentFixture(t *testing.T) {
	if os.Getenv("NT_R11_DEPLOYMENT_VERIFY") != "1" {
		t.Skip("requires isolated restore deployment")
	}
	marker, e := os.ReadFile("/drill/offline/.r11-deployment-context")
	if e != nil || string(marker) != "ephemeral-r11-deployment" {
		t.Fatal("isolated drill guard missing")
	}
	profile, e := runtimeprofile.Read("/drill/runtime/runtime/active.json")
	if e != nil || profile.RestoreID == "" || !profile.WritesEnabled || !strings.Contains(profile.DatabaseURL, "/nt_restore_") {
		t.Fatal("restored deployment is not activated", e)
	}
	ctx := context.Background()
	pool, e := pgxpool.New(ctx, profile.DatabaseURL)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	var count int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM r11_pitr_marker WHERE id='after-archive'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("did not return to backup time", e)
	}
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE state IN ('queued','running')`).Scan(&count); e != nil || count != 0 {
		t.Fatal("old jobs were replayed", e)
	}
	conn, e := pool.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	e = (backup.RecoveryBundle{Version: 1, MFAKey: profile.MFAKey, VaultKey: profile.VaultKey}).Verify(ctx, conn.Conn())
	conn.Release()
	if e != nil {
		t.Fatal(e)
	}
	mfa, _ := hex.DecodeString(profile.MFAKey)
	vault, _ := hex.DecodeString(profile.VaultKey)
	app := New(pool, Config{EncryptionKey: mfa, VaultKey: vault, AllowedOrigins: []string{origin}})
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	raw, e := os.ReadFile("/drill/offline/old-session.json")
	if e != nil {
		t.Fatal(e)
	}
	var old http.Cookie
	if e = json.Unmarshal(raw, &old); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, "http://server:8089", "GET", "/api/admin/credentials", nil, &old, origin), 401)
	fresh := loginAs(t, srv, "accept-admin")
	expect(t, call(t, srv.URL, "GET", "/api/admin/credentials", nil, fresh, origin), 200)
	// The fresh normal password-authenticated session also works on the deployed API.
	expect(t, call(t, "http://server:8089", "GET", "/api/admin/backup/restores", nil, fresh, origin), 200)
	var user string
	var node int64
	if e = pool.QueryRow(ctx, `SELECT user_id::text,id FROM storage_nodes WHERE name='pitr-durable.txt'`).Scan(&user, &node); e != nil {
		t.Fatal(e)
	}
	payload, e := storage.New(app.db).Read(storage.WithActor(ctx, user, "web", ""), user, node, 0, 100)
	if e != nil || string(payload) != "pitr-durable-content" {
		t.Fatal("shared file was not restored", e)
	}
}
