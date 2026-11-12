package application

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/backup"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/storage"
	"next-terminal/backend/migrations"
	"os"
	"path/filepath"
	"testing"
)

func TestR11PITRFixture(t *testing.T) {
	mode := os.Getenv("NT_PITR_MODE")
	if mode == "" {
		t.Skip("requires isolated pgBackRest fixture")
	}
	marker, e := os.ReadFile("/drill/.r11-pitr-context")
	if e != nil || string(marker) != "ephemeral-r11-pitr" {
		t.Fatal("isolated drill guard missing")
	}
	ctx := context.Background()
	pool, e := pgxpool.New(ctx, os.Getenv("NT_PITR_FIXTURE_DSN"))
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	mfa, vault := bytes.Repeat([]byte{42}, 32), bytes.Repeat([]byte{43}, 32)
	cfg := Config{EncryptionKey: mfa, VaultKey: vault, AllowedOrigins: []string{origin}}
	dir := "/drill/offline"
	bundle := backup.RecoveryBundle{Version: 1, MFAKey: hex.EncodeToString(mfa), VaultKey: hex.EncodeToString(vault)}
	if mode == "seed" {
		if e = migrations.Apply(ctx, pool); e != nil {
			t.Fatal(e)
		}
		app := New(pool, cfg)
		srv := httptest.NewServer(app.Handler())
		defer srv.Close()
		expect(t, call(t, srv.URL, "POST", "/api/setup-user", setupBody(), nil, origin), 204)
		cookie := loginAs(t, srv, "accept-admin")
		postID(t, srv, "/api/admin/credentials", credInput("pitr-durable"), cookie)
		if _, e = pool.Exec(ctx, `UPDATE instance_state SET mfa_key_fingerprint=$1,vault_key_fingerprint=$2`, identity.Digest(string(mfa)), identity.Digest(string(vault))); e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, `CREATE TABLE r11_pitr_marker(id text PRIMARY KEY,created_at timestamptz NOT NULL DEFAULT clock_timestamp()); INSERT INTO jobs(kind,state) VALUES('ssh.command','queued')`); e != nil {
			t.Fatal(e)
		}
		var user string
		if e = pool.QueryRow(ctx, `SELECT id::text FROM users WHERE username='accept-admin'`).Scan(&user); e != nil {
			t.Fatal(e)
		}
		fs := storage.New(app.db)
		r11RecoveryEvidence(t, app, srv, cookie, user)
		root, e := fs.Ensure(ctx, user)
		if e != nil {
			t.Fatal(e)
		}
		actor := storage.WithActor(ctx, user, "web", "")
		node, e := fs.Create(actor, user, root.ID, "pitr-durable.txt", false)
		if e != nil {
			t.Fatal(e)
		}
		if e = fs.Write(actor, user, node.ID, 0, []byte("pitr-durable-content")); e != nil {
			t.Fatal(e)
		}
		if e = os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(cookie)
		if e = os.WriteFile(filepath.Join(dir, "old-session.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
		key, e := age.GenerateX25519Identity()
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "identity"), []byte(key.String()), 0600); e != nil {
			t.Fatal(e)
		}
		if e = backup.New(app.db).SaveConfig(ctx, backup.Config{Recipient: key.Recipient().String(), RetentionDays: 31, ScheduleTime: "02:00", Timezone: "Asia/Shanghai", Scope: "full", Revision: 1}); e != nil {
			t.Fatal(e)
		}
		if _, e = backup.CreateSnapshot(ctx, os.Getenv("NT_PITR_FIXTURE_DSN"), dir, filepath.Join(dir, "recovery.age"), key.Recipient().String(), identity.NewToken(), Release, "full", bundle); e != nil {
			t.Fatal(e)
		}
		return
	}
	if mode != "verify" {
		t.Fatal("unknown PITR fixture mode")
	}
	var count int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM r11_pitr_marker WHERE id='keep'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("PITR missed target transaction", e)
	}
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM r11_pitr_marker WHERE id='discard'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("PITR crossed target", e)
	}
	raw, e := os.ReadFile(filepath.Join(dir, "identity"))
	if e != nil {
		t.Fatal(e)
	}
	archive, e := os.Open(filepath.Join(dir, "recovery.age"))
	if e != nil {
		t.Fatal(e)
	}
	defer archive.Close()
	manifest, stage, e := backup.Stage(archive, string(raw), backup.SupportedSchema, dir)
	clear(raw)
	if e != nil {
		t.Fatal(e)
	}
	log, e := backup.NewRestoreLog(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	if e = VerifyRestoreDatabase(ctx, os.Getenv("NT_PITR_FIXTURE_DSN"), stage, manifest, log); e != nil {
		t.Fatal(e)
	}
	app := New(pool, cfg)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	raw, e = os.ReadFile(filepath.Join(dir, "old-session.json"))
	if e != nil {
		t.Fatal(e)
	}
	var old http.Cookie
	if e = json.Unmarshal(raw, &old); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/admin/credentials", nil, &old, origin), 401)
	fresh := loginAs(t, srv, "accept-admin")
	expect(t, call(t, srv.URL, "GET", "/api/admin/credentials", nil, fresh, origin), 200)
	var user string
	var node int64
	if e = pool.QueryRow(ctx, `SELECT user_id::text,id FROM storage_nodes WHERE name='pitr-durable.txt'`).Scan(&user, &node); e != nil {
		t.Fatal(e)
	}
	payload, e := storage.New(app.db).Read(storage.WithActor(ctx, user, "web", ""), user, node, 0, 100)
	if e != nil || string(payload) != "pitr-durable-content" {
		t.Fatal("PITR shared file mismatch", e)
	}
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE state IN ('queued','running')`).Scan(&count); e != nil || count != 0 {
		t.Fatal("PITR replayed old tasks", e)
	}
	if e = log.Append("completed"); e != nil {
		t.Fatal(e)
	}
}
