package backup

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
)

// RestoreStaged accepts only a new, explicitly named staging database owned by
// a non-superuser. It cannot overwrite the application database. Its caller
// must authenticate and Stage the archive before invoking this operation.
func RestoreStaged(ctx context.Context, dsn, dir string, m Manifest, log *RestoreLog) error {
	if e := m.Validate(SupportedSchema); e != nil {
		return e
	}
	if m.SchemaVersion < 30 || m.SchemaVersion > SupportedSchema {
		return errors.New("this release restores schema 30 and 31 archives only")
	}
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil {
		return errors.New("invalid staging connection")
	}
	if !strings.HasPrefix(cfg.Database, "nt_restore_") {
		return errors.New("staging database name must start with nt_restore_")
	}
	c, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		return errors.New("staging database unavailable")
	}
	defer c.Close(context.Background())
	var super, owner bool
	var objects, version int
	if e = c.QueryRow(ctx, `SELECT r.rolsuper, d.datdba=r.oid,current_setting('server_version_num')::int FROM pg_roles r JOIN pg_database d ON d.datname=current_database() WHERE r.rolname=current_user`).Scan(&super, &owner, &version); e != nil {
		return e
	}
	if super || !owner || version/10000 != 18 {
		return errors.New("staging requires PG18 and a non-superuser database owner")
	}
	if e = c.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%'`).Scan(&objects); e != nil {
		return e
	}
	if objects != 0 {
		return errors.New("staging database is not empty")
	}
	if e = log.Append("staging"); e != nil {
		return e
	}
	// Only the empty default schema in this freshly provisioned staging DB.
	if _, e = c.Exec(ctx, "DROP SCHEMA IF EXISTS public"); e != nil {
		return e
	}
	if e = PGTool(ctx, "pg_restore", dsn, dir, []string{"--single-transaction", "--exit-on-error", "--no-owner", "--no-acl", filepath.Join(dir, "database.dump")}, io.Discard); e != nil {
		log.Append("failed")
		return e
	}
	if e = log.Append("verifying"); e != nil {
		return e
	}
	var schema int
	var instance string
	if e = c.QueryRow(ctx, "SELECT schema_version,instance_id FROM instance_state WHERE singleton=1").Scan(&schema, &instance); e != nil {
		return errors.New("restored instance metadata unavailable")
	}
	if schema != m.SchemaVersion || instance != m.InstanceID {
		return errors.New("restored instance does not match manifest")
	}
	return nil
}
