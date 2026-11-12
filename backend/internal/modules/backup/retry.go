package backup

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/platform/fault"
)

func (s *Service) LocalFile(ctx context.Context, id string) (v File, e error) {
	var raw []byte
	e = s.db.Row(ctx, `SELECT id,storage_key,size,sha256,manifest,created_at FROM backup_files WHERE id=$1 AND state='available'`, id).Scan(&v.ID, &v.StorageKey, &v.Size, &v.SHA256, &raw, &v.CreatedAt)
	if e == nil {
		e = json.Unmarshal(raw, &v.Manifest)
	}
	return
}
func (s *Service) RetryCopies(ctx context.Context, id string) error {
	return s.db.Within(ctx, func(c context.Context) error {
		if _, e := s.db.Exec(c, `SELECT pg_advisory_xact_lock(110030)`); e != nil {
			return e
		}
		var state string
		e := s.db.Row(c, `SELECT j.state FROM jobs j JOIN backup_files f ON f.id=j.id WHERE j.id=$1 AND j.kind='backup.create' AND f.state='available' FOR UPDATE OF j,f`, id).Scan(&state)
		if errors.Is(e, pgx.ErrNoRows) {
			return fault.Invalid
		}
		if e != nil {
			return e
		}
		if state == "queued" || state == "running" {
			return nil
		}
		if state != "partial" && state != "unknown" && state != "failed" {
			return fault.Conflict
		}
		var blocked bool
		if e = s.db.Row(c, `SELECT EXISTS(SELECT 1 FROM jobs WHERE kind='backup.create' AND state IN ('queued','running')) OR EXISTS(SELECT 1 FROM backup_copies WHERE task_id=$1 AND reason='restored_requires_review')`, id).Scan(&blocked); e != nil {
			return e
		}
		if blocked {
			return fault.Conflict
		}
		if _, e = s.db.Exec(c, `UPDATE jobs SET state='queued',reason='manual_copy_retry',cancel_requested=false,lease_token=NULL,lease_until=NULL,ended_at=NULL,started_at=NULL WHERE id=$1`, id); e != nil {
			return e
		}
		if _, e = s.db.Exec(c, `UPDATE backup_tasks SET stage='uploading' WHERE id=$1`, id); e != nil {
			return e
		}
		_, e = s.db.Exec(c, `UPDATE backup_copies SET state='pending',reason='' WHERE task_id=$1 AND state IN ('failed','copying','pending')`, id)
		return e
	})
}
