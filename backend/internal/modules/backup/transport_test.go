package backup

import (
	"slices"
	"testing"
)

func TestPostgresTransportDoesNotDowngradeTLS(t *testing.T) {
	t.Setenv("PGSERVICE", "")
	t.Setenv("PGSSLMODE", "verify-full")
	t.Setenv("PGSSLROOTCERT", "/private/ca.pem")
	env, e := pgTransportEnvironment("postgres://backup@db/app")
	if e != nil {
		t.Fatal(e)
	}
	if !slices.Contains(env, "PGSSLMODE=verify-full") || !slices.Contains(env, "PGSSLROOTCERT=/private/ca.pem") {
		t.Fatal("TLS requirements lost")
	}
	env, e = pgTransportEnvironment("postgres://backup@db/app?sslmode=verify-ca&sslrootcert=%2Fother%2Fca.pem")
	if e != nil {
		t.Fatal(e)
	}
	if !slices.Contains(env, "PGSSLMODE=verify-ca") || !slices.Contains(env, "PGSSLROOTCERT=/other/ca.pem") {
		t.Fatal("explicit TLS requirements not used")
	}
	for _, dsn := range []string{"host=db dbname=app", "postgres://db/app?sslmode=disable&sslmode=verify-full", "postgres://db/app?sslmode=unknown"} {
		if _, e = pgTransportEnvironment(dsn); e == nil {
			t.Fatal("unsupported ambiguous connection accepted")
		}
	}
}
