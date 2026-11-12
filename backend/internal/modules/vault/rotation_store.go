package vault

import (
	"context"
	"golang.org/x/crypto/ssh"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/jobs"
)

type RotationRun struct {
	jobs.Job
	ActorID    string           `json:"actorId"`
	ScheduleID string           `json:"scheduleId,omitempty"`
	SourceIP   string           `json:"-"`
	Targets    []RotationTarget `json:"targets,omitempty"`
}

// ActivateRotation is called in the asset-binding transaction after independent
// remote verification. The previous shared credential is never overwritten.
func (s *Service) ActivateRotation(ctx context.Context, t RotationTarget, username string, secret Secret) (string, error) {
	in := Write{Credential: Credential{Name: "rotation-" + t.ID, Kind: t.Kind, Username: username}, Secret: &secret}
	if e := Validate(&in); e != nil {
		return "", e
	}
	id, e := s.Save(ctx, in)
	if e != nil {
		return "", e
	}
	tag, e := s.db.Exec(ctx, `UPDATE rotation_targets SET new_credential_id=$2 WHERE id=$1 AND state IN ('verifying','unknown') AND new_credential_id IS NULL`, t.ID, id)
	if e != nil {
		return "", e
	}
	if tag.RowsAffected() != 1 {
		return "", fault.Conflict
	}
	return id, nil
}

func (s *Service) RotationRun(ctx context.Context, id string) (v RotationRun, e error) {
	e = s.db.Row(ctx, `SELECT j.id,j.kind,j.state,j.reason,j.cancel_requested,j.created_at,j.started_at,j.ended_at,r.actor_id,r.source_ip,coalesce(sr.schedule_id::text,'') FROM rotation_runs r JOIN jobs j ON j.id=r.id LEFT JOIN scheduled_runs sr ON sr.id=r.id WHERE r.id=$1`, id).Scan(&v.ID, &v.Kind, &v.State, &v.Reason, &v.CancelRequested, &v.CreatedAt, &v.StartedAt, &v.EndedAt, &v.ActorID, &v.SourceIP, &v.ScheduleID)
	if e == nil {
		v.Targets, e = s.RotationTargets(ctx, id)
	}
	return
}
func (s *Service) RotationRuns(ctx context.Context, actor string, admin bool, page int) ([]RotationRun, int, error) {
	var total int
	if e := s.db.Row(ctx, `SELECT count(*) FROM rotation_runs WHERE actor_id=$1 OR $2`, actor, admin).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := s.db.Rows(ctx, `SELECT id FROM rotation_runs WHERE actor_id=$1 OR $2 ORDER BY created_at DESC,id LIMIT 30 OFFSET $3`, actor, admin, (page-1)*30)
	if e != nil {
		return nil, 0, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, 0, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, 0, e
	}
	out := []RotationRun{}
	for _, id := range ids {
		v, e := s.RotationRun(ctx, id)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, v)
	}
	return out, total, nil
}
func (s *Service) CreateRotation(ctx context.Context, actor, key, ip string) (string, bool, error) {
	// Transaction-scoped serialization makes a repeated request idempotent even
	// while its first request is still assembling the target snapshots.
	if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, actor+":"+key); e != nil {
		return "", false, e
	}
	var existing string
	e := s.db.Row(ctx, `SELECT id FROM rotation_runs WHERE actor_id=$1 AND request_key=$2`, actor, key).Scan(&existing)
	if e == nil {
		return existing, false, nil
	}
	if !jobs.IsEmpty(e) {
		return "", false, e
	}
	if e = s.db.RequireFeature(ctx, "automation.execute"); e != nil {
		return "", false, e
	}
	id, e := jobs.New(s.db).Enqueue(ctx, "credential.rotate", "queued", "")
	if e != nil {
		return "", false, e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO rotation_runs(id,actor_id,request_key,source_ip) VALUES($1,$2,$3,$4)`, id, actor, key, ip)
	return id, true, e
}
func (s *Service) RotationAuth(ctx context.Context, id, actor, run string) (Credential, ssh.AuthMethod, error) {
	return s.sshAuth(ctx, id, actor, run, "credential_rotation")
}
func (s *Service) RotationSecret(ctx context.Context, id, actor, run string) (Secret, error) {
	var sealed []byte
	if e := s.db.Row(ctx, `SELECT candidate FROM rotation_targets WHERE id=$1 AND run_id=$2`, id, run).Scan(&sealed); e != nil {
		return Secret{}, e
	}
	if _, e := s.db.Exec(ctx, `INSERT INTO secret_access_events(credential_id,version,actor_id,request_id,purpose) SELECT c.id,c.active_version,$2,$3,'rotation_candidate' FROM rotation_targets t JOIN credentials c ON c.id=t.credential_id WHERE t.id=$1`, id, actor, run); e != nil {
		return Secret{}, e
	}
	return s.OpenRotationCandidate(id, sealed)
}

// RotationTarget deliberately excludes encrypted candidates from public DTOs.
type RotationTarget struct {
	ID                string         `json:"id"`
	RunID             string         `json:"runId"`
	AssetID           string         `json:"assetId"`
	AssetVersion      int64          `json:"assetVersion"`
	CredentialID      string         `json:"credentialId"`
	CredentialVersion int64          `json:"credentialVersion"`
	AccountKey        string         `json:"-"`
	Kind              string         `json:"kind"`
	PublicKey         string         `json:"publicKey"`
	NewCredentialID   string         `json:"newCredentialId"`
	State             RotationResult `json:"state"`
	Reason            string         `json:"reason"`
}

// AddRotationTarget requires the caller's transaction and authorization. The
// unique live-account index also serializes assets aliasing the same account.
func (s *Service) AddRotationTarget(ctx context.Context, v RotationTarget) (string, error) {
	var id string
	if e := s.db.Row(ctx, `SELECT gen_random_uuid()`).Scan(&id); e != nil {
		return "", e
	}
	candidate, e := s.NewRotationCandidate(v.Kind, id)
	if e != nil {
		return "", e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO rotation_targets(id,run_id,asset_id,asset_version,credential_id,credential_version,account_key,kind,candidate,public_key) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, v.RunID, v.AssetID, v.AssetVersion, v.CredentialID, v.CredentialVersion, v.AccountKey, v.Kind, candidate.Sealed, candidate.PublicKey)
	return id, e
}

func (s *Service) RotationTargets(ctx context.Context, run string) ([]RotationTarget, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,run_id,asset_id,asset_version,credential_id,credential_version,account_key,kind,public_key,coalesce(new_credential_id::text,''),state,reason FROM rotation_targets WHERE run_id=$1 ORDER BY id`, run)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []RotationTarget{}
	for rows.Next() {
		var v RotationTarget
		if e = rows.Scan(&v.ID, &v.RunID, &v.AssetID, &v.AssetVersion, &v.CredentialID, &v.CredentialVersion, &v.AccountKey, &v.Kind, &v.PublicKey, &v.NewCredentialID, &v.State, &v.Reason); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// MoveRotation holds the execution fence and compares the prior state. Unknown
// reconciliation is deliberately not part of the mutating worker interface.
func (s *Service) MoveRotation(ctx context.Context, job jobs.Job, id string, from, to RotationResult, reason string) error {
	if from == RotationUnknown || !RotationTransition(from, to) {
		return fault.Conflict
	}
	if e := jobs.New(s.db).Owned(ctx, job); e != nil {
		return e
	}
	if to == RotationApplying {
		if e := jobs.New(s.db).Fence(ctx, job); e != nil {
			return e
		}
	}
	tag, e := s.db.Exec(ctx, `UPDATE rotation_targets SET state=$4,reason=$5,updated_at=now() WHERE id=$1 AND run_id=$2 AND state=$3`, id, job.ID, from, to, reason)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}

// RecoverRotation is called in the lease recovery transaction. Candidates are
// retained for independent authentication probes, never requeued for mutation.
func (s *Service) RecoverRotation(ctx context.Context, run string) error {
	_, e := s.db.Exec(ctx, `UPDATE rotation_targets SET state=CASE WHEN state='pending' THEN 'cancelled' ELSE 'unknown' END,reason='worker_stopped',updated_at=now() WHERE run_id=$1 AND state IN ('pending','applying','verifying')`, run)
	return e
}
