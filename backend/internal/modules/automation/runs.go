package automation

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/jobs"
	"time"
)

type Target struct {
	AssetID   string     `json:"assetId"`
	Name      string     `json:"name"`
	State     string     `json:"state"`
	Reason    string     `json:"reason"`
	Output    string     `json:"output"`
	Truncated bool       `json:"truncated"`
	ExitCode  *int       `json:"exitCode"`
	StartedAt *time.Time `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt"`
}
type Run struct {
	jobs.Job
	ScheduleID  string     `json:"scheduleId"`
	ActorID     string     `json:"actorId"`
	ScheduledAt *time.Time `json:"scheduledAt"`
	Snapshot    Schedule   `json:"snapshot"`
	Targets     []Target   `json:"targets,omitempty"`
}

func (s *Service) Run(ctx context.Context, id string) (v Run, e error) {
	var raw []byte
	e = s.db.Row(ctx, `SELECT j.id,j.kind,j.state,j.reason,j.cancel_requested,j.created_at,j.started_at,j.ended_at,r.schedule_id,r.actor_id,r.scheduled_at,r.snapshot FROM jobs j JOIN scheduled_runs r ON r.id=j.id WHERE j.id=$1 AND r.audit_purged_at IS NULL`, id).Scan(&v.ID, &v.Kind, &v.State, &v.Reason, &v.CancelRequested, &v.CreatedAt, &v.StartedAt, &v.EndedAt, &v.ScheduleID, &v.ActorID, &v.ScheduledAt, &raw)
	if e != nil {
		return
	}
	e = json.Unmarshal(raw, &v.Snapshot)
	if e != nil {
		return
	}
	rows, e := s.db.Rows(ctx, `SELECT t.asset_id,rr.display_name,t.state,t.reason,t.output,t.truncated,t.exit_code,t.started_at,t.ended_at FROM scheduled_run_targets t JOIN resource_registry rr ON rr.id=t.asset_id WHERE t.run_id=$1 ORDER BY t.asset_id`, id)
	if e != nil {
		return
	}
	defer rows.Close()
	v.Targets = []Target{}
	for rows.Next() {
		var t Target
		if e = rows.Scan(&t.AssetID, &t.Name, &t.State, &t.Reason, &t.Output, &t.Truncated, &t.ExitCode, &t.StartedAt, &t.EndedAt); e != nil {
			return
		}
		v.Targets = append(v.Targets, t)
	}
	e = rows.Err()
	return
}
func (s *Service) Runs(ctx context.Context, actor string, admin bool, schedule string, page int) ([]Run, int, error) {
	var total int
	filter := `r.audit_purged_at IS NULL AND (r.actor_id=$1 OR $2) AND ($3='' OR r.schedule_id::text=$3)`
	if e := s.db.Row(ctx, `SELECT count(*) FROM scheduled_runs r WHERE `+filter, actor, admin, schedule).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := s.db.Rows(ctx, `SELECT j.id,j.kind,j.state,j.reason,j.cancel_requested,j.created_at,j.started_at,j.ended_at,r.schedule_id,r.actor_id,r.scheduled_at,r.snapshot FROM scheduled_runs r JOIN jobs j ON j.id=r.id WHERE `+filter+` ORDER BY j.created_at DESC,j.id LIMIT 30 OFFSET $4`, actor, admin, schedule, (page-1)*30)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var v Run
		var raw []byte
		if e = rows.Scan(&v.ID, &v.Kind, &v.State, &v.Reason, &v.CancelRequested, &v.CreatedAt, &v.StartedAt, &v.EndedAt, &v.ScheduleID, &v.ActorID, &v.ScheduledAt, &raw); e != nil {
			return nil, 0, e
		}
		if e = json.Unmarshal(raw, &v.Snapshot); e != nil {
			return nil, 0, e
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}
func (s *Service) Dispatch(ctx context.Context, run, asset string) error {
	if e := s.db.RequireFeature(ctx, "automation.execute"); e != nil {
		return e
	}
	tag, e := s.db.Exec(ctx, `UPDATE scheduled_run_targets SET state='running',started_at=now() WHERE run_id=$1 AND asset_id=$2 AND state='queued'`, run, asset)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}

// Caller holds the job fence. A stale worker cannot overwrite recovered outcomes.
func (s *Service) TargetDone(ctx context.Context, run string, t Target) error {
	_, e := s.db.Exec(ctx, `UPDATE scheduled_run_targets SET state=$3,reason=$4,output=$5,truncated=$6,exit_code=$7,ended_at=now() WHERE run_id=$1 AND asset_id=$2 AND state IN ('queued','running')`, run, t.AssetID, t.State, t.Reason, t.Output, t.Truncated, t.ExitCode)
	return e
}
func (s *Service) RecoverRun(ctx context.Context, id string) error {
	if _, e := s.db.Exec(ctx, `UPDATE scheduled_run_targets SET state=CASE WHEN state='running' THEN 'unknown' ELSE 'cancelled' END,reason='worker_stopped',ended_at=now() WHERE run_id=$1 AND state IN ('queued','running')`, id); e != nil {
		return e
	}
	return s.PauseUnknown(ctx, id)
}
func (s *Service) PauseUnknown(ctx context.Context, id string) error {
	_, e := s.db.Exec(ctx, `UPDATE schedules SET enabled=false,next_run=NULL,pause_reason='previous_run_unknown',version=version+1 WHERE id=(SELECT schedule_id FROM scheduled_runs WHERE id=$1) AND EXISTS(SELECT 1 FROM jobs WHERE id=$1 AND state='unknown')`, id)
	return e
}
func (s *Service) DefinitionActive(ctx context.Context, id string) error {
	if e := s.db.RequireFeature(ctx, "automation.execute"); e != nil {
		return e
	}
	return s.DefinitionPresent(ctx, id)
}

// Recovery only inspects an existing definition; it cannot dispatch work.
func (s *Service) DefinitionPresent(ctx context.Context, id string) error {
	var ok bool
	e := s.db.Row(ctx, `SELECT deleted_at IS NULL FROM schedules WHERE id=$1`, id).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return fault.Denied
	}
	return nil
}

// Rotation reconciliation updates only an uncertain task target, never a new
// dispatch. Its caller holds the rotation job and target locks.
func (s *Service) ReconcileTarget(ctx context.Context, run, asset, state string) error {
	if state != "succeeded" && state != "failed" {
		return fault.Invalid
	}
	_, e := s.db.Exec(ctx, `UPDATE scheduled_run_targets SET state=$3,reason='authentication_reconciled',ended_at=now() WHERE run_id=$1 AND asset_id=$2 AND state='unknown'`, run, asset, state)
	return e
}
