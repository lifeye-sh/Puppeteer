package pgproxy

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/modules/databaseauth"
	"next-terminal/backend/internal/modules/pki"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTargetTLSConfiguration(t *testing.T) {
	pem, key, e := pki.Generate("fixture", []string{"db.internal"}, 1, true, nil)
	clear(key)
	if e != nil {
		t.Fatal(e)
	}
	target := Target{Address: "db.internal:5432", User: "reader", Password: "password", Database: "inventory", ServerName: "db.internal", CA: string(pem), Timeout: time.Second}
	t.Setenv("PGOPTIONS", "-c default_transaction_read_only=off")
	c, e := targetConfig(target)
	if e != nil {
		t.Fatal(e)
	}
	if c.TLSConfig == nil || c.TLSConfig.InsecureSkipVerify || c.TLSConfig.MinVersion < tls.VersionTLS12 || len(c.Fallbacks) != 0 || c.RequireAuth != "scram-sha-256" || len(c.RuntimeParams) != 4 || c.RuntimeParams["options"] != "" {
		t.Fatal("unsafe target configuration")
	}
	for _, change := range []func(*Target){func(t *Target) { t.CA = "" }, func(t *Target) { t.ServerName = "" }, func(t *Target) { t.Address = "/tmp:5432" }, func(t *Target) { t.Timeout = 0 }, func(t *Target) { t.Address = "db.internal:70000" }} {
		v := target
		change(&v)
		if _, e := targetConfig(v); e == nil {
			t.Fatal("invalid target accepted")
		}
	}
}
func TestPostgreSQL18TLSAndReadonlyPrivileges(t *testing.T) {
	directory := os.Getenv("NT_R16_PG_FIXTURE")
	if directory == "" {
		t.Skip("isolated R16 PostgreSQL fixture required")
	}
	pem, e := os.ReadFile(filepath.Join(directory, "server.pem"))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	target := Target{Address: "r16-postgres-fixture:5432", User: "reader", Password: "r16-fixture-reader", Database: "inventory", ServerName: "r16-postgres-fixture", CA: string(pem), Timeout: 5 * time.Second}
	c, e := ConnectTarget(ctx, target)
	if e != nil {
		t.Fatal(e)
	}
	var count int
	if e = c.QueryRow(ctx, "SELECT count(*) FROM public.items").Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
	if _, e = c.Exec(ctx, "INSERT INTO public.items VALUES(99,'must not write')"); e == nil {
		t.Fatal("write accepted")
	}
	c.Close(ctx)
	for _, v := range []Target{
		func() Target { v := target; v.User = "writer"; v.Password = "r16-fixture-writer"; return v }(),
		func() Target { v := target; v.User = "member"; v.Password = "r16-fixture-member"; return v }(),
		func() Target { v := target; v.User = "postgres"; v.Password = "r16-disposable-fixture-only"; return v }(),
	} {
		c, e := ConnectTarget(ctx, v)
		if c != nil {
			c.Close(ctx)
		}
		if !errors.Is(e, ErrPrivileges) {
			t.Fatalf("unsafe role %s: %v", v.User, e)
		}
	}

	program := target
	program.User, program.Password, program.ProgramPermissions = "writer", "r16-fixture-writer", true
	pc, err := ConnectTarget(ctx, program)
	if err != nil {
		t.Fatal("program mode rejected broader account", err)
	}
	if _, err = pc.Exec(ctx, "INSERT INTO public.items VALUES(99,'must not write')"); err == nil {
		pc.Close(ctx)
		t.Fatal("program readonly accepted write")
	}
	pc.Close(ctx)
	bad := target
	bad.ServerName = "wrong.internal"
	if c, e := ConnectTarget(ctx, bad); e == nil {
		c.Close(ctx)
		t.Fatal("TLS name bypass")
	}
	bad = target
	bad.Password = "wrong"
	if c, e := ConnectTarget(ctx, bad); e == nil {
		c.Close(ctx)
		t.Fatal("bad password accepted")
	}
	// Effective PUBLIC, column-level and function privileges must also be rejected.
	admin, e := pgx.Connect(ctx, "postgresql://postgres:r16-disposable-fixture-only@r16-postgres-fixture:5432/inventory?sslmode=disable")
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close(ctx)
	password, bundle, e := databaseauth.NewVerifier().Create(ctx)
	if e != nil {
		t.Fatal(e)
	}
	material, e := databaseauth.SCRAMCredentials(bundle)
	if e != nil {
		t.Fatal(e)
	}
	stored := fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", material.Iterations, base64.StdEncoding.EncodeToString(material.Salt), base64.StdEncoding.EncodeToString(material.StoredKey), base64.StdEncoding.EncodeToString(material.ServerKey))
	if _, e = admin.Exec(ctx, "ALTER ROLE reader PASSWORD '"+stored+"'"); e != nil {
		t.Fatal(e)
	}
	generated := target
	generated.Password = password
	verified, authError := ConnectTarget(ctx, generated)
	if verified != nil {
		verified.Close(ctx)
	}
	if _, e = admin.Exec(ctx, "ALTER ROLE reader PASSWORD 'r16-fixture-reader'"); e != nil {
		t.Fatal(e)
	}
	if authError != nil {
		t.Fatal("generated SCRAM material incompatible with PostgreSQL", authError)
	}
	for _, pair := range [][2]string{
		{"GRANT TEMPORARY ON DATABASE inventory TO PUBLIC", "REVOKE TEMPORARY ON DATABASE inventory FROM PUBLIC"},
		{"GRANT UPDATE(label) ON public.items TO reader", "REVOKE UPDATE(label) ON public.items FROM reader"},
		{"CREATE FUNCTION public.unsafe_fixture() RETURNS int LANGUAGE sql AS 'SELECT 1'", "DROP FUNCTION public.unsafe_fixture()"},
	} {
		if _, e = admin.Exec(ctx, pair[0]); e != nil {
			t.Fatal(e)
		}
		c, probe := ConnectTarget(ctx, target)
		if c != nil {
			c.Close(ctx)
		}
		if _, e = admin.Exec(ctx, pair[1]); e != nil {
			t.Fatal(e)
		}
		if !errors.Is(probe, ErrPrivileges) {
			t.Fatalf("unsafe privilege accepted: %v", probe)
		}
	}
	c, e = ConnectTarget(ctx, target)
	if e != nil {
		t.Fatal("fixture not restored", e)
	}
	c.Close(ctx)
}
