package audit

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
)

// Caller owns the ended session lock and the retention configuration lock.
func (s *Service) PurgeAccess(ctx context.Context, id string) error {
	if !s.db.InTransaction(ctx) {
		return fault.Invalid
	}
	if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(680069)`); e != nil {
		return e
	}
	var held bool
	if e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM audit_holds WHERE category='access' AND record_id=$1)`, id).Scan(&held); e != nil {
		return e
	}
	if held {
		return fault.Conflict
	}
	if _, e := s.db.Exec(ctx, `UPDATE jobs SET cancel_requested=true WHERE id IN (SELECT job_id FROM recording_exports WHERE session_id=$1) AND state IN ('queued','running')`, id); e != nil {
		return e
	}
	var locked string
	if e := s.db.Row(ctx, `SELECT session_id FROM recordings WHERE session_id=$1 FOR UPDATE`, id).Scan(&locked); e != nil {
		return e
	}
	for _, query := range []string{`DELETE FROM native_rdp_tickets WHERE session_id=$1`, `DELETE FROM native_forward_streams WHERE session_id=$1`, `DELETE FROM recording_chunks WHERE session_id=$1`, `DELETE FROM recordings WHERE session_id=$1`, `DELETE FROM session_commands WHERE session_id=$1`, `DELETE FROM access_logs WHERE session_id=$1`} {
		if _, e := s.db.Exec(ctx, query, id); e != nil {
			return e
		}
	}
	return nil
}

// Prevent retention deletion from racing with a complete recording export.
func (s *Service) LockRecordingExport(ctx context.Context, id string) error {
	var locked string
	return s.db.Row(ctx, `SELECT session_id FROM recordings WHERE session_id=$1 FOR SHARE`, id).Scan(&locked)
}

// PurgeSessionContent retains the access summary; its parent access hold protects
// both lifecycles. Ended sessions only, under the same retention/export locks.
func (s *Service) PurgeSessionContent(ctx context.Context, id string) error {
	if !s.db.InTransaction(ctx) {
		return fault.Invalid
	}
	if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(680069)`); e != nil {
		return e
	}
	var locked string
	if e := s.db.Row(ctx, `SELECT id FROM sessions WHERE id=$1 AND closed_at IS NOT NULL AND audit_purged_at IS NULL AND recording_purged_at IS NULL FOR UPDATE`, id).Scan(&locked); e != nil {
		return e
	}
	var held bool
	if e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM audit_holds WHERE category='access' AND record_id=$1)`, id).Scan(&held); e != nil {
		return e
	}
	if held {
		return fault.Conflict
	}
	if _, e := s.db.Exec(ctx, `UPDATE jobs SET cancel_requested=true WHERE id IN (SELECT job_id FROM recording_exports WHERE session_id=$1) AND state IN ('queued','running')`, id); e != nil {
		return e
	}
	if e := s.db.Row(ctx, `SELECT session_id FROM recordings WHERE session_id=$1 FOR UPDATE`, id).Scan(&locked); e != nil {
		return e
	}
	for _, q := range []string{`DELETE FROM recording_exports WHERE session_id=$1`, `DELETE FROM recording_chunks WHERE session_id=$1`, `DELETE FROM session_commands WHERE session_id=$1`, `DELETE FROM native_forward_streams WHERE session_id=$1`, `UPDATE recordings SET state='expired',events=0,bytes=0,sha256='',integrity_state='unsealed' WHERE session_id=$1`, `UPDATE sessions SET recording_purged_at=now() WHERE id=$1`} {
		if _, e := s.db.Exec(ctx, q, id); e != nil {
			return e
		}
	}
	return nil
}
