package migrations

import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"sort"
	"strconv"
	"strings"
)

//go:embed *.sql
var files embed.FS

// CurrentVersion is derived from the migrations shipped in this binary, so
// read-only tools cannot silently retain an obsolete, separately copied number.
// It does not apply migrations or change database state.
func CurrentVersion() (int, error) {
	entries, err := files.ReadDir(".")
	if err != nil {
		return 0, err
	}
	latest := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := strconv.Atoi(strings.Split(entry.Name(), "_")[0])
		if err != nil || version < 1 {
			return 0, fmt.Errorf("invalid migration version: %s", entry.Name())
		}
		if version > latest {
			latest = version
		}
	}
	if latest == 0 {
		return 0, fmt.Errorf("no embedded migrations")
	}
	return latest, nil
}

func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(78201601)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS migration_history(version integer PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := files.ReadDir(".")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, e := strconv.Atoi(strings.Split(entry.Name(), "_")[0])
		if e != nil {
			return e
		}
		sql, err := files.ReadFile(entry.Name())
		if err != nil {
			return err
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(sql))
		var existing string
		if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT checksum FROM migration_history WHERE version=$1),'')`, version).Scan(&existing); err != nil {
			return err
		}
		if existing != "" && existing != sum {
			return fmt.Errorf("migration %d checksum mismatch; do not edit applied migrations", version)
		}
		if existing == "" {
			if _, err = tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO migration_history(version,checksum) VALUES($1,$2)`, version, sum); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}
