package vault

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
)

// LockReconcile excludes a running worker and concurrent activation requests.
func (s *Service) LockReconcile(ctx context.Context, run, id string) error {
	var state string
	if e := s.db.Row(ctx, `SELECT state FROM jobs WHERE id=$1 FOR UPDATE`, run).Scan(&state); e != nil {
		return e
	}
	if state != "unknown" && state != "partial" {
		return fault.Conflict
	}
	if e := s.db.Row(ctx, `SELECT state FROM rotation_targets WHERE id=$1 AND run_id=$2 FOR UPDATE`, id, run).Scan(&state); e != nil {
		return e
	}
	if state != "unknown" {
		return fault.Conflict
	}
	return nil
}
func (s *Service) ResolveRotation(ctx context.Context, run, id string, result RotationResult) error {
	if result != RotationSucceeded && result != RotationFailed {
		return fault.Invalid
	}
	tag, e := s.db.Exec(ctx, `UPDATE rotation_targets SET state=$3,reason='authentication_reconciled',updated_at=now() WHERE run_id=$1 AND id=$2 AND state='unknown'`, run, id, result)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}
