package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrAdmission = errors.New("execution entitlement unavailable")

func (d *DB) RequireFeature(ctx context.Context, feature string) error {
	if err := d.Admit(ctx, "", feature); err != nil {
		return fmt.Errorf("%w: %w", ErrAdmission, err)
	}
	return nil
}

// RunAdmitted bounds revocation latency for external calls. Never run a watcher
// against a transaction: pgx transactions cannot execute concurrent queries.
// Cancellation cannot roll back an external side effect; callers retain unknown
// outcomes and must not automatically retry a revoked dispatch.
func (d *DB) RunAdmitted(parent context.Context, feature string, call func(context.Context) error) error {
	if d.InTransaction(parent) {
		return errors.New("external admitted call inside transaction")
	}
	if err := d.RequireFeature(parent, feature); err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(parent)
	done := make(chan struct{})
	defer func() { cancel(nil); <-done }()
	go func() {
		defer close(done)
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				check, stop := context.WithTimeout(ctx, 2*time.Second)
				err := d.RequireFeature(check, feature)
				stop()
				if err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	err := call(ctx)
	if cause := context.Cause(ctx); errors.Is(cause, ErrAdmission) {
		return cause
	}
	if ctx.Err() != nil {
		if err != nil {
			return err
		}
		return ctx.Err()
	}
	if check := d.RequireFeature(ctx, feature); check != nil {
		return check
	}
	return err
}
