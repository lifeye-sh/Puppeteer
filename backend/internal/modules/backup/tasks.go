package backup

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/jobs"
	"time"
)

type Task struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	Stage     string    `json:"stage"`
	Remark    string    `json:"remark"`
	CreatedAt time.Time `json:"createdAt"`
	Config    Config    `json:"config"`
}

func (s *Service) Request(ctx context.Context, actor, key string) (id string, e error) {
	e = s.db.Within(ctx, func(c context.Context) error {
		if _, err := s.db.Exec(c, "SELECT pg_advisory_xact_lock(110030)"); err != nil {
			return err
		}
		err := s.db.Row(c, `SELECT id::text FROM backup_tasks WHERE actor_id IS NOT DISTINCT FROM NULLIF($1,'')::uuid AND request_key=$2`, actor, key).Scan(&id)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var active bool
		if err = s.db.Row(c, `SELECT EXISTS(SELECT 1 FROM jobs WHERE kind='backup.create' AND state IN ('queued','running'))`).Scan(&active); err != nil {
			return err
		}
		if active {
			return fault.Conflict
		}
		cfg, err := s.Config(c)
		if err != nil {
			return err
		}
		if err = ValidateConfig(cfg); err != nil {
			return err
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		id, err = jobs.New(s.db).Enqueue(c, "backup.create", "queued", "")
		if err != nil {
			return err
		}
		_, err = s.db.Exec(c, `INSERT INTO backup_tasks(id,actor_id,request_key,config_snapshot) VALUES($1,NULLIF($2,'')::uuid,$3,$4)`, id, actor, key, raw)
		if err != nil {
			return err
		}
		return s.snapshotDestinations(c, id)
	})
	return
}
func (s *Service) Task(ctx context.Context, id string) (v Task, e error) {
	var raw []byte
	e = s.db.Row(ctx, `SELECT b.id::text,j.state,b.stage,b.remark,b.created_at,b.config_snapshot FROM backup_tasks b JOIN jobs j ON j.id=b.id WHERE b.id=$1`, id).Scan(&v.ID, &v.State, &v.Stage, &v.Remark, &v.CreatedAt, &raw)
	if e == nil {
		e = json.Unmarshal(raw, &v.Config)
	}
	return
}
func (s *Service) Tasks(ctx context.Context) ([]Task, error) {
	rows, e := s.db.Rows(ctx, `SELECT b.id::text,j.state,b.stage,b.remark,b.created_at,b.config_snapshot FROM backup_tasks b JOIN jobs j ON j.id=b.id ORDER BY b.created_at DESC LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var v Task
		var raw []byte
		if e = rows.Scan(&v.ID, &v.State, &v.Stage, &v.Remark, &v.CreatedAt, &raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &v.Config); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Stage(ctx context.Context, j jobs.Job, stage string) error {
	return s.db.Within(ctx, func(c context.Context) error {
		if e := jobs.New(s.db).Fence(c, j); e != nil {
			return e
		}
		_, e := s.db.Exec(c, `UPDATE backup_tasks SET stage=$2 WHERE id=$1`, j.ID, stage)
		return e
	})
}
func (s *Service) Complete(ctx context.Context, j jobs.Job, f File) error {
	return s.db.Within(ctx, func(c context.Context) error {
		if e := jobs.New(s.db).Fence(c, j); e != nil {
			return e
		}
		raw, e := json.Marshal(f.Manifest)
		if e != nil {
			return e
		}
		if _, e = s.db.Exec(c, `INSERT INTO backup_files(id,storage_key,size,sha256,manifest) VALUES($1,$2,$3,$4,$5)`, j.ID, f.StorageKey, f.Size, f.SHA256, raw); e != nil {
			return e
		}
		if _, e = s.db.Exec(c, `UPDATE backup_tasks SET stage='uploading' WHERE id=$1`, j.ID); e != nil {
			return e
		}
		return nil
	})
}
func (s *Service) QuarantineRestore(ctx context.Context) error {
	for _, q := range []string{`UPDATE backup_config SET schedule_enabled=false,revision=revision+1`, `DELETE FROM backup_export_tickets`, `DELETE FROM backup_recipient_proofs`, `UPDATE backup_destinations SET enabled=false,revision=revision+1`, `UPDATE backup_copies SET state='failed',reason='restored_requires_review' WHERE state<>'deleted'`, `UPDATE backup_files SET state='missing'`, `UPDATE backup_tasks SET stage='interrupted',remark='restored_requires_review' WHERE stage NOT IN ('succeeded','failed')`} {
		if _, e := s.db.Exec(ctx, q); e != nil {
			return e
		}
	}
	return nil
}
