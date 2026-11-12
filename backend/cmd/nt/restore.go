package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"next-terminal/backend/internal/application"
	"next-terminal/backend/internal/modules/backup"
	"next-terminal/backend/internal/platform/deployment"
	"next-terminal/backend/migrations"
)

// prepareRestore never swaps a running database. The next maintenance stage
// must create a safety backup and explicitly switch deployment configuration.
func prepareRestore(args []string) (err error) {
	f := flag.NewFlagSet("restore prepare", flag.ContinueOnError)
	archive := f.String("archive", "", "trusted encrypted backup")
	identity := f.String("identity-file", "", "offline recovery key file")
	parent := f.String("workspace", "", "private independent restore workspace")
	trusted := f.Bool("trust-source", false, "acknowledge that the dump can execute source-defined SQL")
	if err = f.Parse(args); err != nil {
		return err
	}
	if *archive == "" || *identity == "" || *parent == "" || !*trusted || f.NArg() != 0 {
		return errors.New("archive, identity-file, workspace and --trust-source required")
	}
	if err = deployment.LoadSecrets(); err != nil {
		return err
	}
	dsn := os.Getenv("NT_RESTORE_DATABASE_URL")
	if dsn == "" {
		return errors.New("NT_RESTORE_DATABASE_URL must name a new isolated nt_restore_ database")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.Database, "nt_restore_") {
		return errors.New("invalid isolated staging target")
	}
	log, err := backup.NewRestoreLog(*parent)
	if err != nil {
		return err
	}
	defer log.Close()
	defer func() {
		if err != nil {
			log.Append("failed")
		}
	}()
	keyFile, err := os.Open(*identity)
	if err != nil {
		return err
	}
	key, err := io.ReadAll(io.LimitReader(keyFile, 4097))
	keyFile.Close()
	if err != nil || len(key) > 4096 {
		return errors.New("invalid offline identity file")
	}
	file, err := os.Open(*archive)
	if err != nil {
		return err
	}
	defer file.Close()
	m, dir, err := backup.Stage(file, strings.TrimSpace(string(key)), backup.SupportedSchema, *parent)
	clear(key)
	if err != nil {
		return err
	}
	if err = log.Append("validated"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err = backup.RestoreStaged(ctx, dsn, dir, m, log); err != nil {
		return err
	}
	keyFile, err = os.Open(filepath.Join(dir, "keys", "recovery-bundle.json"))
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(keyFile, 16385))
	keyFile.Close()
	if err != nil {
		return err
	}
	bundle, err := backup.ParseRecoveryBundle(raw)
	clear(raw)
	if err != nil {
		return err
	}
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return errors.New("staging database unavailable")
	}
	err = bundle.Verify(ctx, c)
	if err == nil {
		err = backup.VerifyObjects(ctx, c, dir, m.Scope)
	}
	c.Close(ctx)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = migrations.Apply(ctx, pool); err != nil {
		return err
	}
	mfa, _ := hex.DecodeString(bundle.MFAKey)
	vault, _ := hex.DecodeString(bundle.VaultKey)
	defer clear(mfa)
	defer clear(vault)
	app := application.New(pool, application.Config{EncryptionKey: mfa, VaultKey: vault})
	if err = app.QuarantineRestoredState(ctx, log.ID, m.Scope); err != nil {
		return err
	}
	if err = log.Append("prepared"); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"restoreId": log.ID, "state": "prepared", "database": cfg.Database, "workspace": dir, "switched": false})
}
