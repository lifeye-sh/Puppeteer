package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type txKey struct{}

func (d *DB) InTransaction(ctx context.Context) bool {
	_, ok := ctx.Value(txKey{}).(pgx.Tx)
	return ok
}

type DB struct {
	pool      *pgxpool.Pool
	admission func(context.Context, string, string) error
}

func New(pool *pgxpool.Pool) *DB { return &DB{pool: pool} }

// SetAdmissionCheck is wired once during application construction, before any
// requests or workers start. Domain services share this unit-of-work boundary.
func (d *DB) SetAdmissionCheck(check func(context.Context, string, string) error) {
	d.admission = check
}
func (d *DB) Admit(ctx context.Context, category, feature string) error {
	if d.admission == nil {
		return nil
	}
	return d.admission(ctx, category, feature)
}

// PoolStats exposes counters only, never connection strings or pool handles.
func (d *DB) PoolStats() *pgxpool.Stat { return d.pool.Stat() }

type querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (d *DB) query(ctx context.Context) querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return d.pool
}
func (d *DB) Exec(ctx context.Context, q string, a ...any) (pgconn.CommandTag, error) {
	return d.query(ctx).Exec(ctx, q, a...)
}
func (d *DB) Row(ctx context.Context, q string, a ...any) pgx.Row {
	return d.query(ctx).QueryRow(ctx, q, a...)
}
func (d *DB) Rows(ctx context.Context, q string, a ...any) (pgx.Rows, error) {
	return d.query(ctx).Query(ctx, q, a...)
}
func (d *DB) Within(ctx context.Context, fn func(context.Context) error) error {
	tx, e := d.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = fn(context.WithValue(ctx, txKey{}, tx)); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
