package application

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/url"
	"next-terminal/backend/internal/modules/backup"
	"next-terminal/backend/internal/platform/runtimeprofile"
	"path/filepath"
	"strings"
)

func EnsureRestoreTarget(ctx context.Context, adminDSN string, plan RestorePlan) error {
	target, e := url.Parse(plan.Next.DatabaseURL)
	if e != nil || !strings.HasPrefix(plan.Database, "nt_restore_") || !strings.HasPrefix(plan.Role, "nt_restore_role_") || target.Path != "/"+plan.Database || target.User.Username() != plan.Role {
		return errors.New("invalid restore target")
	}
	password, ok := target.User.Password()
	if !ok || len(password) < 32 || strings.ContainsAny(password, "'\\") {
		return errors.New("invalid restore role password")
	}
	admin, e := pgx.Connect(ctx, adminDSN)
	if e != nil {
		return errors.New("restore administrator connection unavailable")
	}
	defer admin.Close(context.Background())
	if e = backup.EnsureReaderRole(ctx, admin); e != nil {
		return e
	}
	var exists, safe bool
	e = admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1),EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1 AND rolcanlogin AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls)`, plan.Role).Scan(&exists, &safe)
	if e != nil {
		return e
	}
	if exists && !safe {
		return errors.New("existing restore role is not restricted")
	}
	if !exists {
		if _, e = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{plan.Role}.Sanitize()+" LOGIN PASSWORD '"+password+"'"); e != nil {
			return errors.New("unable to create restricted restore role")
		}
	}
	if _, e = admin.Exec(ctx, "GRANT "+pgx.Identifier{backup.ReaderRole}.Sanitize()+" TO "+pgx.Identifier{plan.Role}.Sanitize()); e != nil {
		return e
	}
	var owner string
	e = admin.QueryRow(ctx, `SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname=$1`, plan.Database).Scan(&owner)
	if errors.Is(e, pgx.ErrNoRows) {
		_, e = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{plan.Database}.Sanitize()+" OWNER "+pgx.Identifier{plan.Role}.Sanitize())
		return e
	}
	if e != nil {
		return e
	}
	if owner != plan.Role {
		return errors.New("existing restore target owner mismatch")
	}
	return nil
}
func ResumeRestoreTarget(ctx context.Context, adminDSN, parent string, plan *RestorePlan, log *backup.RestoreLog) error {
	if e := backup.VerifyStagedFiles(plan.Stage, plan.Manifest); e != nil {
		return e
	}
	if e := EnsureRestoreTarget(ctx, adminDSN, *plan); e != nil {
		return e
	}
	fence, e := FenceOffline(ctx, plan.Next.DatabaseURL)
	if e != nil {
		return e
	}
	defer fence.Close(context.Background())
	var restored bool
	if e = fence.QueryRow(ctx, `SELECT to_regclass('public.instance_state') IS NOT NULL`).Scan(&restored); e != nil {
		return e
	}
	if !restored {
		if e = backup.RestoreStaged(ctx, plan.Next.DatabaseURL, plan.Stage, plan.Manifest, log); e != nil {
			return e
		}
	}
	if e = VerifyRestoreDatabase(ctx, plan.Next.DatabaseURL, plan.Stage, plan.Manifest, log); e != nil {
		return e
	}
	plan.Verified = true
	return runtimeprofile.AtomicJSON(filepath.Join(parent, plan.ID, "plan.json"), plan)
}
