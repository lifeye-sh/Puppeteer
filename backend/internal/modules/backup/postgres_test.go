package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPostgresIsolatedSnapshotRestore(t *testing.T) {
	dsn := os.Getenv("NT_BACKUP_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated PG18 tool fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	root, e := pgx.Connect(ctx, dsn)
	if e != nil {
		t.Fatal("test database unavailable")
	}
	defer root.Close(context.Background())
	random := make([]byte, 12)
	rand.Read(random)
	suffix := hex.EncodeToString(random)
	source := "r11_source_" + suffix
	target := "r11_target_" + suffix
	role := "r11_role_" + suffix
	for _, name := range []string{source, target} {
		if _, e = root.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); e != nil {
			t.Fatal(e)
		}
		defer root.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	}
	cfg, _ := pgx.ParseConfig(dsn)
	cfg.Database = source
	src, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	_, e = src.Exec(ctx, "CREATE TABLE recovery_fixture(id integer PRIMARY KEY, value text NOT NULL); INSERT INTO recovery_fixture VALUES(1,'snapshot-value')")
	src.Close(ctx)
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	dump, e := DumpSnapshot(ctx, testDSN(cfg), dir)
	if e != nil {
		t.Fatal(e)
	}
	// Non-superuser restoration into an otherwise empty, separate database.
	password := suffix + suffix
	if _, e = root.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN PASSWORD '"+password+"'"); e != nil {
		t.Fatal(e)
	}
	defer func() {
		root.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{target}.Sanitize()+" WITH (FORCE)")
		root.Exec(context.Background(), "DROP ROLE "+pgx.Identifier{role}.Sanitize())
	}()
	if _, e = root.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{target}.Sanitize()+" OWNER TO "+pgx.Identifier{role}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	cfg.Database = target
	cfg.User = role
	cfg.Password = password
	staging, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	_, e = staging.Exec(ctx, "DROP SCHEMA public")
	staging.Close(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = PGTool(ctx, "pg_restore", testDSN(cfg), dir, []string{"--exit-on-error", "--no-owner", "--no-acl", dump}, io.Discard); e != nil {
		t.Fatal(e)
	}
	restored, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close(ctx)
	var value string
	if e = restored.QueryRow(ctx, "SELECT value FROM recovery_fixture WHERE id=1").Scan(&value); e != nil || value != "snapshot-value" {
		t.Fatal("restored data mismatch", e)
	}
	if e = PGTool(ctx, "pg_restore", testDSN(cfg), dir, []string{"--clean", dump}, io.Discard); e == nil {
		t.Fatal("destructive restore accepted")
	}
}

func testDSN(c *pgx.ConnConfig) string {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(c.User, c.Password), Host: net.JoinHostPort(c.Host, strconv.Itoa(int(c.Port))), Path: "/" + c.Database, RawQuery: "sslmode=disable"}
	return u.String()
}
