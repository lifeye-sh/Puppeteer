package application

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/deployment"
	"next-terminal/backend/internal/platform/postgres"
	"next-terminal/backend/internal/platform/runtimeprofile"
	"next-terminal/backend/migrations"
	"os"
)

func Open(ctx context.Context) (*pgxpool.Pool, error) {
	if e := deployment.LoadSecrets(); e != nil {
		return nil, e
	}
	if e := runtimeprofile.Apply(); e != nil {
		return nil, e
	}
	if e := deployment.Validate(os.Getenv); e != nil {
		return nil, e
	}
	if os.Getenv("NT_ENV") == "production" {
		if e := deployment.CheckStorage(); e != nil {
			return nil, e
		}
	}
	dsn := os.Getenv("NT_DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("NT_DATABASE_URL is required")
	}
	c, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		return nil, errors.New("invalid database configuration")
	}
	c.MaxConns = 10
	c.MinConns = 1
	c.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, e := conn.Exec(ctx, `SELECT pg_advisory_lock_shared(110031)`)
		return e
	}
	p, e := pgxpool.NewWithConfig(ctx, c)
	if e != nil {
		return nil, e
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, errors.New("database unavailable")
	}
	if e = migrations.Apply(ctx, p); e != nil {
		p.Close()
		return nil, e
	}
	key, e := hex.DecodeString(os.Getenv("NT_MFA_KEY"))
	if e != nil || len(key) != 32 {
		p.Close()
		return nil, errors.New("NT_MFA_KEY must contain 32 random bytes as hex")
	}
	if e = system.New(postgres.New(p)).CheckMFAKey(ctx, identity.Digest(string(key))); e != nil {
		p.Close()
		return nil, errors.New("MFA encryption key does not match this database; restore the original local key")
	}
	vaultKey, e := hex.DecodeString(os.Getenv("NT_VAULT_KEY"))
	if e != nil || len(vaultKey) != 32 {
		p.Close()
		return nil, errors.New("NT_VAULT_KEY must contain 32 random bytes as hex")
	}
	if e = system.New(postgres.New(p)).CheckVaultKey(ctx, identity.Digest(string(vaultKey))); e != nil {
		p.Close()
		return nil, errors.New("vault encryption key mismatch; restore the original local key")
	}
	return p, nil

}
