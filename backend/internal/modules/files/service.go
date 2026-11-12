package files

import (
	"bytes"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"time"
)

const MaxFile = 16 * 1024 * 1024
const MaxChunk = 1024 * 1024

type Service struct{ db *postgres.DB }

func New(db *postgres.DB) *Service { return &Service{db} }

type Task struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	ActorID   string    `json:"-"`
	AssetID   string    `json:"-"`
	Root      string    `json:"-"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	Received  int64     `json:"received"`
	Expected  string    `json:"-"`
	Hash      string    `json:"sha256"`
	State     string    `json:"state"`
	Cancel    bool      `json:"cancelRequested"`
	Cleanup   bool      `json:"cleanupNeeded"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

const taskCols = `id,session_id,actor_id,asset_id,root,path,size,received,expected_hash,content_hash,state,cancel_requested,cleanup_needed,message,created_at`

func scanTask(row pgx.Row) (v Task, e error) {
	e = row.Scan(&v.ID, &v.SessionID, &v.ActorID, &v.AssetID, &v.Root, &v.Path, &v.Size, &v.Received, &v.Expected, &v.Hash, &v.State, &v.Cancel, &v.Cleanup, &v.Message, &v.CreatedAt)
	return
}
func (s *Service) Task(ctx context.Context, id, owner string) (Task, error) {
	return scanTask(s.db.Row(ctx, `SELECT `+taskCols+` FROM upload_tasks WHERE id=$1 AND actor_id=$2`, id, owner))
}
func (s *Service) Mount(ctx context.Context, id, root string) (string, error) {
	var saved string
	e := s.db.Row(ctx, `INSERT INTO filesystem_mounts(session_id,root) VALUES($1,$2) ON CONFLICT(session_id) DO UPDATE SET root=filesystem_mounts.root RETURNING root`, id, root).Scan(&saved)
	return saved, e
}
func (s *Service) Create(ctx context.Context, v Task) (Task, error) {
	if e := s.db.RequireFeature(ctx, "files.write"); e != nil {
		return v, e
	}
	e := s.db.Within(ctx, func(ctx context.Context) error {
		if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(905001)`); e != nil {
			return e
		}
		existing, e := s.Task(ctx, v.ID, v.ActorID)
		if e == nil {
			if existing.SessionID != v.SessionID || existing.Path != v.Path || existing.Size != v.Size || existing.Expected != v.Expected {
				return fault.Conflict
			}
			v = existing
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		var reserved, global int64
		var count int
		e = s.db.Row(ctx, `SELECT COALESCE(sum(size) FILTER(WHERE actor_id=$1),0),COALESCE(sum(size),0),count(*) FILTER(WHERE actor_id=$1) FROM upload_tasks WHERE state IN ('receiving','transferring','committing')`, v.ActorID).Scan(&reserved, &global, &count)
		if e != nil {
			return e
		}
		if reserved+v.Size > 64*1024*1024 || global+v.Size > 256*1024*1024 || count >= 4 {
			return fault.Conflict
		}
		_, e = s.db.Exec(ctx, `INSERT INTO upload_tasks(id,session_id,actor_id,asset_id,root,path,size,expected_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, v.ID, v.SessionID, v.ActorID, v.AssetID, v.Root, v.Path, v.Size, v.Expected)
		return e
	})
	if e != nil {
		return v, e
	}
	return s.Task(ctx, v.ID, v.ActorID)
}
func (s *Service) Chunk(ctx context.Context, id, owner string, offset int64, data []byte) error {
	if e := s.db.RequireFeature(ctx, "files.write"); e != nil {
		return e
	}
	if len(data) == 0 || len(data) > MaxChunk || offset < 0 {
		return fault.Invalid
	}
	return s.db.Within(ctx, func(ctx context.Context) error {
		v, e := scanTask(s.db.Row(ctx, `SELECT `+taskCols+` FROM upload_tasks WHERE id=$1 AND actor_id=$2 FOR UPDATE`, id, owner))
		if e != nil {
			return e
		}
		if v.State != "receiving" || v.Cancel {
			return fault.Conflict
		}
		if offset < v.Received {
			var old []byte
			e = s.db.Row(ctx, `SELECT payload FROM upload_chunks WHERE task_id=$1 AND offset_bytes=$2`, id, offset).Scan(&old)
			if e == nil && bytes.Equal(old, data) {
				return nil
			}
			return fault.Conflict
		}
		if offset != v.Received || offset+int64(len(data)) > v.Size {
			return fault.Conflict
		}
		if _, e = s.db.Exec(ctx, `INSERT INTO upload_chunks VALUES($1,$2,$3)`, id, offset, data); e != nil {
			return e
		}
		_, e = s.db.Exec(ctx, `UPDATE upload_tasks SET received=received+$2,updated_at=now() WHERE id=$1`, id, len(data))
		return e
	})
}
func (s *Service) Claim(ctx context.Context, id, owner string) error {
	if e := s.db.RequireFeature(ctx, "files.write"); e != nil {
		return e
	}
	tag, e := s.db.Exec(ctx, `UPDATE upload_tasks SET state='transferring',updated_at=now() WHERE id=$1 AND actor_id=$2 AND state='receiving' AND received=size AND NOT cancel_requested AND expires_at>now()`, id, owner)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}
func (s *Service) Data(ctx context.Context, id string) ([]byte, error) {
	rows, e := s.db.Rows(ctx, `SELECT payload FROM upload_chunks WHERE task_id=$1 ORDER BY offset_bytes`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var data []byte
	for rows.Next() {
		var chunk []byte
		if e = rows.Scan(&chunk); e != nil {
			return nil, e
		}
		if len(data)+len(chunk) > MaxFile {
			return nil, fault.Invalid
		}
		data = append(data, chunk...)
	}
	return data, rows.Err()
}
func (s *Service) Committing(ctx context.Context, id string) error {
	if e := s.db.RequireFeature(ctx, "files.write"); e != nil {
		return e
	}
	tag, e := s.db.Exec(ctx, `UPDATE upload_tasks SET state='committing',updated_at=now() WHERE id=$1 AND state='transferring' AND NOT cancel_requested`, id)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}
func (s *Service) Finish(ctx context.Context, id, state, message, hash string) error {
	return s.db.Within(ctx, func(ctx context.Context) error {
		if _, e := s.db.Exec(ctx, `UPDATE upload_tasks SET state=$2,message=$3,content_hash=$4,updated_at=now() WHERE id=$1`, id, state, message, hash); e != nil {
			return e
		}
		_, e := s.db.Exec(ctx, `DELETE FROM upload_chunks WHERE task_id=$1`, id)
		return e
	})
}
func (s *Service) Cancel(ctx context.Context, id, owner string) error {
	return s.db.Within(ctx, func(ctx context.Context) error {
		v, e := scanTask(s.db.Row(ctx, `SELECT `+taskCols+` FROM upload_tasks WHERE id=$1 AND actor_id=$2 FOR UPDATE`, id, owner))
		if e != nil {
			return e
		}
		if v.State == "committing" || v.State == "succeeded" {
			return fault.Conflict
		}
		if v.State == "transferring" {
			_, e = s.db.Exec(ctx, `UPDATE upload_tasks SET cancel_requested=true WHERE id=$1`, id)
			return e
		}
		if v.State == "receiving" {
			if _, e = s.db.Exec(ctx, `UPDATE upload_tasks SET state='canceled',cancel_requested=true,updated_at=now() WHERE id=$1`, id); e != nil {
				return e
			}
			_, e = s.db.Exec(ctx, `DELETE FROM upload_chunks WHERE task_id=$1`, id)
		}
		return e
	})
}
func (s *Service) SetCleanup(ctx context.Context, id string, needed bool) error {
	_, e := s.db.Exec(ctx, `UPDATE upload_tasks SET cleanup_needed=$2 WHERE id=$1`, id, needed)
	return e
}
func (s *Service) CleanupTasks(ctx context.Context, asset, actor, root string) ([]Task, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,path FROM upload_tasks WHERE asset_id=$1 AND actor_id=$2 AND root=$3 AND cleanup_needed AND state IN ('failed','canceled','interrupted','succeeded') LIMIT 100`, asset, actor, root)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var v Task
		if e = rows.Scan(&v.ID, &v.Path); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) LockAsset(ctx context.Context, id string, fn func(context.Context) error) error {
	return s.db.Within(ctx, func(ctx context.Context) error {
		var locked bool
		if e := s.db.Row(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,905005))`, id).Scan(&locked); e != nil {
			return e
		}
		if !locked {
			return fault.Conflict
		}
		return fn(ctx)
	})
}

// The single-process deployment recovers in-flight tasks on restart. Remote leftovers
// remain tracked and are removed on the next authorized mount of the same asset/root.
func (s *Service) Recover(ctx context.Context, startup bool) error {
	return s.db.Within(ctx, func(ctx context.Context) error {
		if _, e := s.db.Exec(ctx, `UPDATE upload_tasks SET state='interrupted',message='传输中断；提交阶段需核对目标内容后再重试',updated_at=now() WHERE ($1 AND state IN ('transferring','committing')) OR (state='receiving' AND (expires_at<now() OR EXISTS(SELECT 1 FROM sessions s WHERE s.id=session_id AND s.closed_at IS NOT NULL)))`, startup); e != nil {
			return e
		}
		_, e := s.db.Exec(ctx, `DELETE FROM upload_chunks c USING upload_tasks t WHERE c.task_id=t.id AND t.state NOT IN ('receiving','transferring','committing')`)
		return e
	})
}
