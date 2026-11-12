package jobs

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"time"
)

type Queue struct{ db *postgres.DB }

func New(db *postgres.DB) *Queue { return &Queue{db} }

type Job struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	State           string     `json:"state"`
	Reason          string     `json:"reason"`
	Token           string     `json:"-"`
	CancelRequested bool       `json:"cancelRequested"`
	CreatedAt       time.Time  `json:"createdAt"`
	StartedAt       *time.Time `json:"startedAt"`
	EndedAt         *time.Time `json:"endedAt"`
}
type Handler func(context.Context, Job) error

// Registry is assembled before workers start. Unknown kinds are never dispatched.
type Registry map[string]Handler

func (q *Queue) Enqueue(ctx context.Context, kind, state, reason string) (id string, e error) {
	e = q.db.Row(ctx, `INSERT INTO jobs(kind,state,reason,ended_at) VALUES($1,$2,$3,CASE WHEN $2='skipped' THEN now() END) RETURNING id`, kind, state, reason).Scan(&id)
	return
}
func (q *Queue) Claim(ctx context.Context, kind string) (j Job, e error) {
	e = q.db.Row(ctx, `UPDATE jobs SET state='running',lease_token=gen_random_uuid(),lease_until=now()+interval '30 seconds',started_at=now() WHERE id=(SELECT id FROM jobs WHERE state='queued' AND kind=$1 AND NOT cancel_requested ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,kind,state,lease_token::text`, kind).Scan(&j.ID, &j.Kind, &j.State, &j.Token)
	return
}
func (q *Queue) Heartbeat(ctx context.Context, j Job) error {
	tag, e := q.db.Exec(ctx, `UPDATE jobs SET lease_until=now()+interval '30 seconds' WHERE id=$1 AND lease_token=$2 AND state='running' AND NOT cancel_requested AND lease_until>now()`, j.ID, j.Token)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}

// Fence must be called in the same transaction as recording a target dispatch.
func (q *Queue) Fence(ctx context.Context, j Job) error {
	var ok bool
	e := q.db.Row(ctx, `SELECT NOT cancel_requested AND lease_until>now() AND state='running' AND lease_token=$2 FROM jobs WHERE id=$1 FOR UPDATE`, j.ID, j.Token).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return fault.Conflict
	}
	return nil
}
func (q *Queue) Cancel(ctx context.Context, id string) error {
	_, e := q.db.Exec(ctx, `UPDATE jobs SET cancel_requested=true WHERE id=$1 AND state IN ('queued','running')`, id)
	return e
}
func (q *Queue) Owned(ctx context.Context, j Job) error {
	var ok bool
	e := q.db.Row(ctx, `SELECT state='running' AND lease_token=$2 AND lease_until>now() FROM jobs WHERE id=$1 FOR UPDATE`, j.ID, j.Token).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return fault.Conflict
	}
	return nil
}
func (q *Queue) Finish(ctx context.Context, j Job, state, reason string) error {
	tag, e := q.db.Exec(ctx, `UPDATE jobs SET state=$3,reason=$4,ended_at=now(),lease_until=NULL WHERE id=$1 AND lease_token=$2 AND state='running' AND lease_until>now()`, j.ID, j.Token, state, reason)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}

// Recover never resets a running job to queued: a remote side effect may already exist.
func (q *Queue) Recover(ctx context.Context) ([]string, error) {
	rows, e := q.db.Rows(ctx, `UPDATE jobs SET state=CASE WHEN state='queued' THEN 'cancelled' ELSE 'unknown' END,reason=CASE WHEN state='queued' THEN 'cancelled_before_start' ELSE 'worker_lease_lost' END,ended_at=now(),lease_until=NULL WHERE (state='running' AND lease_until<now()) OR (state='queued' AND cancel_requested) RETURNING id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func IsEmpty(e error) bool { return errors.Is(e, pgx.ErrNoRows) }

// Reconcile records evidence for a finished uncertain job. The application
// transaction owns the job lock and validates every affected business target.
func (q *Queue) Reconcile(ctx context.Context, id, state string) error {
	if state != "unknown" && state != "succeeded" && state != "failed" && state != "partial" {
		return fault.Invalid
	}
	tag, e := q.db.Exec(ctx, `UPDATE jobs SET state=$2,reason='authentication_reconciled' WHERE id=$1 AND state IN ('unknown','partial')`, id, state)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}

// RecoverResult handles a lost completion acknowledgement when every business
// target already has a durable terminal outcome. It never revives a job.
func (q *Queue) RecoverResult(ctx context.Context, id, state string) error {
	if state != "succeeded" && state != "failed" && state != "partial" && state != "cancelled" {
		return fault.Invalid
	}
	_, e := q.db.Exec(ctx, `UPDATE jobs SET state=$2,reason='durable_targets_recovered' WHERE id=$1 AND state='unknown'`, id, state)
	return e
}
