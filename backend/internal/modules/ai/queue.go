package ai

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/secretbox"
)

type Job struct {
	Prepared      Prepared
	UserID        string
	Auth          string
	Authorization string
	SourceIP      string
	RequestID     string
	ContextDigest string
}

func (s *Service) Enqueue(ctx context.Context, v Job) error {
	if e := s.RequireExecution(ctx); e != nil {
		return e
	}
	// Never persist a model API key. Original authentication is encrypted and
	// validated again at execution time; logout/revocation invalidates queued work.
	v.Prepared.Profile.APIKey = ""
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	sealed, e := secretbox.Seal(s.key, raw, "ai-job:"+v.Prepared.Turn.ID)
	clear(raw)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO ai_jobs(turn_id,state,payload) VALUES($1,'queued',$2)`, v.Prepared.Turn.ID, sealed)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `UPDATE ai_turns SET state='queued',deadline=now()+interval '15 minutes',revision=revision+1 WHERE id=$1`, v.Prepared.Turn.ID)
	return e
}
func (s *Service) Claim(ctx context.Context) (v Job, e error) {
	e = s.db.Within(ctx, func(c context.Context) error {
		var id string
		var encrypted []byte
		if err := s.db.Row(c, `SELECT turn_id,payload FROM ai_jobs WHERE state='queued' AND deadline>now() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &encrypted); err != nil {
			return err
		}
		raw, err := secretbox.Open(s.key, encrypted, "ai-job:"+id)
		if err == nil {
			err = json.Unmarshal(raw, &v)
		}
		clear(raw)
		if err != nil || v.Prepared.Turn.ID != id {
			v = Job{}
			if _, e := s.db.Exec(c, `UPDATE ai_jobs SET state='interrupted',payload=''::bytea WHERE turn_id=$1`, id); e != nil {
				return e
			}
			_, e := s.db.Exec(c, `UPDATE ai_turns SET state='interrupted',reason='request_interrupted',revision=revision+1 WHERE id=$1 AND state='queued'`, id)
			return e
		}
		tag, err := s.db.Exec(c, `UPDATE ai_turns SET state='running',revision=revision+1,deadline=now()+interval '100 seconds' WHERE id=$1 AND state='queued' AND NOT cancel_requested`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fault.Conflict
		}
		_, err = s.db.Exec(c, `UPDATE ai_jobs SET state='running',started_at=now(),deadline=now()+interval '100 seconds' WHERE turn_id=$1`, id)
		v.Prepared.Turn.State = "running"
		return err
	})
	if e == nil && v.Prepared.Turn.ID == "" {
		e = NoJob
	}
	return
}
func (s *Service) CompleteJob(ctx context.Context, id string) error {
	_, e := s.db.Exec(ctx, `UPDATE ai_jobs SET state='finished',payload=''::bytea WHERE turn_id=$1`, id)
	return e
}
func (s *Service) RecoverJobs(ctx context.Context) error {
	return s.db.Within(ctx, func(c context.Context) error {
		// Never replay an in-flight call with unknown external outcome.
		if _, e := s.db.Exec(c, `UPDATE ai_turns SET state='interrupted',reason='request_interrupted',revision=revision+1 WHERE state IN ('queued','running') AND deadline<now()`); e != nil {
			return e
		}
		if _, e := s.db.Exec(c, `UPDATE ai_jobs j SET state='interrupted',payload=''::bytea FROM ai_turns t WHERE t.id=j.turn_id AND t.state NOT IN ('queued','running') AND j.state IN ('queued','running')`); e != nil {
			return e
		}
		if _, e := s.db.Exec(c, `UPDATE ai_tool_executions SET state=CASE WHEN state='running' THEN 'unknown' ELSE 'timed_out' END,reason=CASE WHEN state='running' THEN 'unknown_remote_outcome' ELSE 'approval_expired' END,finished_at=now(),revision=revision+1 WHERE (state IN ('awaiting_approval','approved') AND expires_at<now()) OR (state='running' AND started_at<now()-interval '100 seconds')`); e != nil {
			return e
		}
		_, e := s.db.Exec(c, `DELETE FROM ai_events WHERE created_at<now()-interval '7 days'`)
		return e
	})
}
func (s *Service) EventCursor(ctx context.Context, user, id string) (int64, error) {
	if _, e := s.Conversation(ctx, user, id); e != nil {
		return 0, e
	}
	var seq int64
	e := s.db.Row(ctx, `SELECT coalesce(max(seq),0) FROM ai_events WHERE conversation_id=$1`, id).Scan(&seq)
	return seq, e
}

var NoJob = pgx.ErrNoRows
