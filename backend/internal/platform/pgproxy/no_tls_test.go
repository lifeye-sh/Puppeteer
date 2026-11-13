package pgproxy

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestExplicitNoTLSPostgres(t *testing.T) {
	target := Target{Address: "puppeteer-no-tls-postgres:5432", User: "reader", Password: "fixture-reader-only", Database: "inventory", TLSMode: "disable", Timeout: 5 * time.Second}
	cfg, e := targetConfig(target)
	if e != nil {
		t.Fatal(e)
	}
	if cfg.TLSConfig != nil || len(cfg.Fallbacks) != 0 || cfg.RequireAuth != "scram-sha-256" {
		t.Fatal("unsafe auth or fallback")
	}
	invalid := target
	invalid.TLSMode = "prefer"
	if _, e = targetConfig(invalid); e == nil {
		t.Fatal("invalid mode")
	}
	invalid.TLSMode = ""
	if _, e = targetConfig(invalid); e == nil {
		t.Fatal("default downgraded")
	}
	if os.Getenv("NT_TEST_NO_TLS") != "1" {
		t.Skip("isolated no-TLS fixture required")
	}
	ctx := context.Background()
	c, e := ConnectTarget(ctx, target)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close(ctx)
	var n int
	if e = c.QueryRow(ctx, "SELECT count(*) FROM public.items").Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	var ssl bool
	if e = c.QueryRow(ctx, "SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid()").Scan(&ssl); e != nil || ssl {
		t.Fatal("unexpected TLS", e)
	}
	if _, e = c.Exec(ctx, "INSERT INTO public.items VALUES(2,'denied')"); e == nil {
		t.Fatal("readonly accepted write")
	}
}
