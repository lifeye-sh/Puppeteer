package audit

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/jobs"
)

type Export struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	State     string `json:"state"`
	Reason    string `json:"reason"`
	Progress  int    `json:"progress"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

func (s *Service) Export(ctx context.Context, id string) (v Export, e error) {
	v.SessionID = id
	v.State = "none"
	e = s.db.Row(ctx, `SELECT e.job_id::text,j.state,j.reason,e.progress,e.size,e.sha256 FROM recording_exports e JOIN jobs j ON j.id=e.job_id WHERE session_id=$1`, id).Scan(&v.ID, &v.State, &v.Reason, &v.Progress, &v.Size, &v.SHA256)
	if errors.Is(e, pgx.ErrNoRows) {
		e = nil
	}
	return
}
func (s *Service) RequestExport(ctx context.Context, id, actor string) (v Export, e error) {
	e = s.db.Within(ctx, func(c context.Context) error {
		// Serializes deduplication and the small per-instance queue quota.
		if _, e := s.db.Exec(c, `SELECT pg_advisory_xact_lock(110032)`); e != nil {
			return e
		}
		if e := s.LockRecordingExport(c, id); e != nil {
			return e
		}
		old, e := s.Export(c, id)
		if e != nil {
			return e
		}
		if old.State == "queued" || old.State == "running" || old.State == "succeeded" {
			v = old
			return nil
		}
		var count int
		if e = s.db.Row(c, `SELECT count(*) FROM jobs WHERE kind='recording.mp4' AND state IN ('queued','running')`).Scan(&count); e != nil {
			return e
		}
		if count >= 8 {
			return fault.Conflict
		}
		job, e := jobs.New(s.db).Enqueue(c, "recording.mp4", "queued", "")
		if e != nil {
			return e
		}
		_, e = s.db.Exec(c, `INSERT INTO recording_exports(session_id,job_id,actor_id) VALUES($1,$2,$3) ON CONFLICT(session_id) DO UPDATE SET job_id=excluded.job_id,actor_id=excluded.actor_id,progress=0,attempts=0,size=0,sha256='',updated_at=now()`, id, job, actor)
		if e != nil {
			return e
		}
		v = Export{ID: job, SessionID: id, State: "queued"}
		return s.Detail(c, actor, "recording.mp4_requested", "mp4:"+job, "local", map[string]string{"sessionId": id, "jobId": job})
	})
	return
}
func (s *Service) ExportProgress(ctx context.Context, j jobs.Job, progress int) error {
	return s.db.Within(ctx, func(c context.Context) error {
		if e := jobs.New(s.db).Fence(c, j); e != nil {
			return e
		}
		tag, e := s.db.Exec(c, `UPDATE recording_exports SET progress=$2,updated_at=now() WHERE job_id=$1`, j.ID, progress)
		if e == nil && tag.RowsAffected() != 1 {
			return fault.Conflict
		}
		return e
	})
}
func (s *Service) CompleteExport(ctx context.Context, j jobs.Job, size int64, hash string) error {
	// Caller holds the publication transaction and its job fence.
	tag, e := s.db.Exec(ctx, `UPDATE recording_exports SET progress=100,size=$2,sha256=$3,updated_at=now() WHERE job_id=$1`, j.ID, size, hash)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return jobs.New(s.db).Finish(ctx, j, "succeeded", "")
}
