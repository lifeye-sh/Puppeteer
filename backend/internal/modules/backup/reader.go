package backup

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

const ReaderRole = "nt_r11_backup_reader"

func EnsureReaderRole(ctx context.Context, c *pgx.Conn) error {
	// The role cannot log in and never owns data. Existing privileged or inherited
	// roles are rejected instead of silently downgrading an administrator's role.
	var exists bool
	if e := c.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, ReaderRole).Scan(&exists); e != nil {
		return e
	}
	if !exists {
		if _, e := c.Exec(ctx, `DO $$ BEGIN CREATE ROLE nt_r11_backup_reader NOLOGIN; EXCEPTION WHEN duplicate_object THEN NULL; END $$`); e != nil {
			return errors.New("backup reader role provisioning requires a database administrator")
		}
	}
	var safe bool
	if e := c.QueryRow(ctx, `SELECT NOT(rolsuper OR rolcanlogin OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls) AND NOT EXISTS(SELECT 1 FROM pg_auth_members WHERE member=r.oid) FROM pg_roles r WHERE rolname=$1`, ReaderRole).Scan(&safe); e != nil {
		return e
	}
	if !safe {
		return errors.New("backup reader role has excessive privileges")
	}
	return nil
}
func PrepareReader(ctx context.Context, c *pgx.Conn) error {
	if e := EnsureReaderRole(ctx, c); e != nil {
		return e
	}
	var member bool
	if e := c.QueryRow(ctx, `SELECT pg_has_role(current_user,$1,'SET')`, ReaderRole).Scan(&member); e != nil {
		return e
	}
	if !member {
		return errors.New("application role requires membership in the backup reader role")
	}
	if _, e := c.Exec(ctx, `GRANT USAGE ON SCHEMA public TO nt_r11_backup_reader; GRANT SELECT ON ALL TABLES IN SCHEMA public TO nt_r11_backup_reader; GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO nt_r11_backup_reader`); e != nil {
		return e
	}
	var unsafe bool
	if e := c.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON c.relnamespace=n.oid WHERE n.nspname='public' AND c.relkind IN ('r','p') AND has_table_privilege($1,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE'))`, ReaderRole).Scan(&unsafe); e != nil {
		return e
	}
	if unsafe {
		return errors.New("backup reader must not have write privileges")
	}
	return nil
}
