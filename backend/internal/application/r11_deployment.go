package application

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/url"
	"next-terminal/backend/internal/modules/backup"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/runtimeprofile"
	"next-terminal/backend/migrations"
	"os"
	"path/filepath"
	"strings"
)

type RestorePlan struct {
	Manifest      backup.Manifest        `json:"manifest"`
	Verified      bool                   `json:"verified"`
	ID            string                 `json:"id"`
	Old           runtimeprofile.Profile `json:"old"`
	Next          runtimeprofile.Profile `json:"next"`
	Database      string                 `json:"database"`
	Role          string                 `json:"role"`
	Stage         string                 `json:"stage"`
	Scope         string                 `json:"scope"`
	SafetyArchive string                 `json:"safetyArchive"`
}

func readRecoveryBundle(dir string) (backup.RecoveryBundle, error) {
	f, e := os.Open(filepath.Join(dir, "keys", "recovery-bundle.json"))
	if e != nil {
		return backup.RecoveryBundle{}, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, 16385))
	if e != nil {
		return backup.RecoveryBundle{}, e
	}
	defer clear(raw)
	return backup.ParseRecoveryBundle(raw)
}
func VerifyRestoreDatabase(ctx context.Context, dsn, dir string, m backup.Manifest, log *backup.RestoreLog) error {
	bundle, e := readRecoveryBundle(dir)
	if e != nil {
		return e
	}
	c, e := pgx.Connect(ctx, dsn)
	if e != nil {
		return errors.New("restore verification database unavailable")
	}
	var schema int
	var instance string
	e = c.QueryRow(ctx, `SELECT schema_version,instance_id FROM instance_state WHERE singleton=1`).Scan(&schema, &instance)
	if e == nil && ((schema != m.SchemaVersion && schema != backup.SupportedSchema) || instance != m.InstanceID) {
		e = errors.New("restore target metadata mismatch")
	}
	if e == nil {
		e = bundle.Verify(ctx, c)
	}
	if e == nil {
		var quarantined bool
		e = c.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM restore_quarantine WHERE id=$1)`, log.ID).Scan(&quarantined)
		if e == nil {
			if quarantined {
				e = backup.VerifyPermanentObjects(ctx, c, dir, m.Scope)
			} else {
				e = backup.VerifyObjects(ctx, c, dir, m.Scope)
			}
		}
	}
	c.Close(ctx)
	if e != nil {
		return e
	}
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		return e
	}
	defer pool.Close()
	if e = migrations.Apply(ctx, pool); e != nil {
		return e
	}
	var admins int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE type='admin' AND status='enabled' AND deleted_at IS NULL`).Scan(&admins); e != nil {
		return e
	}
	if admins == 0 {
		return errors.New("restored database has no enabled administrator")
	}
	mfa, _ := hex.DecodeString(bundle.MFAKey)
	vault, _ := hex.DecodeString(bundle.VaultKey)
	defer clear(mfa)
	defer clear(vault)
	return New(pool, Config{EncryptionKey: mfa, VaultKey: vault}).QuarantineRestoredState(ctx, log.ID, m.Scope)
}

// FenceOffline requires the API and graphical storage processes to be stopped.
// Runtime pools hold a shared advisory lock; the CLI holds the exclusive lock
// until both database and key profiles have been written.
func FenceOffline(ctx context.Context, dsn string) (*pgx.Conn, error) {
	c, e := pgx.Connect(ctx, dsn)
	if e != nil {
		return nil, errors.New("source database unavailable")
	}
	ok := false
	defer func() {
		if !ok {
			c.Close(context.Background())
		}
	}()
	var locked bool
	if e = c.QueryRow(ctx, `SELECT pg_try_advisory_lock(110031)`).Scan(&locked); e != nil || !locked {
		return nil, errors.New("stop API and graphical services before deployment restore")
	}
	var active int
	if e = c.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND backend_type='client backend'`).Scan(&active); e != nil {
		return nil, e
	}
	if active != 0 {
		return nil, errors.New("database clients are still connected")
	}
	ok = true
	return c, nil
}
func PrepareDeploymentRestore(ctx context.Context, archive, identityFile, parent, profilePath, storagePath, adminDSN string, source runtimeprofile.Profile) (plan RestorePlan, err error) {
	if profilePath == "" || storagePath == "" || adminDSN == "" {
		return plan, errors.New("restore deployment paths and administrator connection required")
	}
	if err = os.MkdirAll(parent, 0700); err != nil {
		return
	}
	log, err := backup.NewRestoreLog(parent)
	if err != nil {
		return
	}
	defer log.Close()
	plan.ID = log.ID
	defer func() {
		if err != nil {
			_ = log.Append("failed")
		}
	}()
	folder := filepath.Join(parent, log.ID)
	key, e := os.Open(identityFile)
	if e != nil {
		return plan, e
	}
	raw, e := io.ReadAll(io.LimitReader(key, 4097))
	key.Close()
	if e != nil || len(raw) > 4096 {
		return plan, errors.New("invalid recovery identity file")
	}
	defer clear(raw)
	file, e := os.Open(archive)
	if e != nil {
		return plan, e
	}
	defer file.Close()
	manifest, stage, e := backup.Stage(file, strings.TrimSpace(string(raw)), backup.SupportedSchema, folder)
	if e != nil {
		return plan, e
	}
	if manifest.SchemaVersion < 30 || manifest.SchemaVersion > backup.SupportedSchema {
		return plan, errors.New("archive schema is outside this release's supported restore range")
	}
	if e = log.Append("validated"); e != nil {
		return plan, e
	}
	fence, e := FenceOffline(ctx, source.DatabaseURL)
	if e != nil {
		return plan, e
	}
	defer fence.Close(context.Background())
	var recipient string
	if e = fence.QueryRow(ctx, `SELECT recipient FROM backup_config WHERE singleton=true`).Scan(&recipient); e != nil {
		return plan, e
	}
	if e = log.Append("safety_backup"); e != nil {
		return plan, e
	}
	safety := filepath.Join(folder, "safety.ntbackup.age")
	if _, e = backup.CreateSnapshot(ctx, source.DatabaseURL, folder, safety, recipient, "safety-"+log.ID, Release, "full", backup.RecoveryBundle{Version: 1, MFAKey: source.MFAKey, VaultKey: source.VaultKey}); e != nil {
		return plan, errors.New("safety backup failed; original deployment unchanged")
	}
	suffix := identity.NewToken()[:16]
	dbName := "nt_restore_" + suffix
	role := "nt_restore_role_" + suffix
	target, e := url.Parse(adminDSN)
	if e != nil || target.Host == "" {
		return plan, errors.New("restore administrator URI required")
	}
	target.Path = "/" + dbName
	target.User = url.UserPassword(role, identity.NewToken())
	bundle, e := readRecoveryBundle(stage)
	if e != nil {
		return plan, e
	}
	plan = RestorePlan{ID: log.ID, Old: source, Next: runtimeprofile.Profile{DatabaseURL: target.String(), MFAKey: bundle.MFAKey, VaultKey: bundle.VaultKey, RestoreID: log.ID, WritesEnabled: false}, Database: dbName, Role: role, Stage: stage, Scope: manifest.Scope, SafetyArchive: safety, Manifest: manifest}
	// Persist provisioning intent before creating any roles or databases.
	if e = runtimeprofile.AtomicJSON(filepath.Join(folder, "plan.json"), plan); e != nil {
		return plan, e
	}
	if e = ResumeRestoreTarget(ctx, adminDSN, parent, &plan, log); e != nil {
		return plan, e
	}
	if e = log.Append("prepared"); e != nil {
		return plan, e
	}
	if e = log.Append("switching"); e != nil {
		return plan, e
	}
	if e = runtimeprofile.AtomicJSON(storagePath, runtimeprofile.Storage{DatabaseURL: plan.Next.DatabaseURL, RestoreID: plan.ID}); e != nil {
		return plan, e
	}
	if e = runtimeprofile.Write(profilePath, plan.Next); e != nil {
		return plan, e
	}
	return plan, nil
}
