package database

import (
	"context"
	"crypto/sha256"
	"fmt"
	"next-terminal/backend/internal/platform/fault"
	"time"
)

type WorkOrder struct {
	ID                string     `json:"id"`
	AssetID           string     `json:"assetId"`
	RequesterID       string     `json:"requesterId"`
	ReviewerID        string     `json:"reviewerId"`
	Title             string     `json:"title"`
	SQL               string     `json:"sql"`
	Hash              string     `json:"hash"`
	Mode              string     `json:"mode"`
	AssetVersion      int64      `json:"assetVersion"`
	CredentialVersion int64      `json:"credentialVersion"`
	State             string     `json:"state"`
	Version           int64      `json:"version"`
	RunID             string     `json:"runId"`
	CancelRequested   bool       `json:"cancelRequested"`
	Reason            string     `json:"reason"`
	CreatedAt         time.Time  `json:"createdAt"`
	ReviewedAt        *time.Time `json:"reviewedAt"`
	StartedAt         *time.Time `json:"startedAt"`
	FinishedAt        *time.Time `json:"finishedAt"`
}

const workFields = `id::text,asset_id::text,requester_id::text,coalesce(reviewer_id::text,''),title,sql_text,sql_sha256,mode,asset_version,credential_version,state,version,coalesce(run_id::text,''),cancel_requested,reason,created_at,reviewed_at,started_at,finished_at`

type scanner interface{ Scan(...any) error }

func scanWork(row scanner) (v WorkOrder, e error) {
	e = row.Scan(&v.ID, &v.AssetID, &v.RequesterID, &v.ReviewerID, &v.Title, &v.SQL, &v.Hash, &v.Mode, &v.AssetVersion, &v.CredentialVersion, &v.State, &v.Version, &v.RunID, &v.CancelRequested, &v.Reason, &v.CreatedAt, &v.ReviewedAt, &v.StartedAt, &v.FinishedAt)
	return
}
func SQLHash(query string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(query))) }
func (s *Service) Asset(ctx context.Context, id string) (v Asset, e error) {
	e = s.db.Row(ctx, `SELECT `+assetFields+` FROM database_assets d JOIN resource_registry r ON r.id=d.id WHERE d.id=$1 AND r.deleted_at IS NULL AND r.enabled`, id).Scan(&v.ID, &v.Name, &v.Alias, &v.Host, &v.Port, &v.Database, &v.ReadonlyCredentialID, &v.WriteCredentialID, &v.TLSServerName, &v.TLSCA, &v.Description, &v.ConnectTimeout, &v.Enabled, &v.Version, &v.Engine, &v.NativeWriteCredentialID, &v.TLSMode, &v.PermissionMode)
	return
}
func (s *Service) CreateWork(ctx context.Context, v WorkOrder) (string, error) {
	if v.Title == "" || len(v.Title) > 200 || len(v.SQL) > MaxSQLBytes {
		return "", fault.Invalid
	}
	var id string
	e := s.db.Row(ctx, `INSERT INTO database_work_orders(asset_id,requester_id,title,sql_text,sql_sha256,mode,asset_version,credential_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, v.AssetID, v.RequesterID, v.Title, v.SQL, SQLHash(v.SQL), v.Mode, v.AssetVersion, v.CredentialVersion).Scan(&id)
	return id, e
}
func (s *Service) Work(ctx context.Context, id string, lock bool) (WorkOrder, error) {
	q := `SELECT ` + workFields + ` FROM database_work_orders WHERE id=$1`
	if lock {
		q += ` FOR UPDATE`
	}
	return scanWork(s.db.Row(ctx, q, id))
}
func (s *Service) Works(ctx context.Context, user, state string, all bool, offset, limit int) ([]WorkOrder, int, error) {
	var total int
	e := s.db.Row(ctx, `SELECT count(*) FROM database_work_orders WHERE ($1 OR requester_id=$2) AND ($3='' OR state=$3)`, all, user, state).Scan(&total)
	if e != nil {
		return nil, 0, e
	}
	rows, e := s.db.Rows(ctx, `SELECT `+workFields+` FROM database_work_orders WHERE ($1 OR requester_id=$2) AND ($3='' OR state=$3) ORDER BY created_at DESC,id LIMIT $4 OFFSET $5`, all, user, state, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []WorkOrder{}
	for rows.Next() {
		v, e := scanWork(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}
func (s *Service) ReviewWork(ctx context.Context, v WorkOrder, reviewer, state, reason string) error {
	if v.RequesterID == reviewer || v.State != "pending" || (state != "approved" && state != "rejected") || len(reason) > 2000 {
		return fault.Denied
	}
	tag, e := s.db.Exec(ctx, `UPDATE database_work_orders SET reviewer_id=$2,state=$3,reason=$4,reviewed_at=now(),version=version+1 WHERE id=$1 AND state='pending' AND version=$5`, v.ID, reviewer, state, reason, v.Version)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}
func (s *Service) CancelWork(ctx context.Context, v WorkOrder) error {
	tag, e := s.db.Exec(ctx, `UPDATE database_work_orders SET cancel_requested=true,state=CASE WHEN state='running' THEN state ELSE 'cancelled' END,finished_at=CASE WHEN state='running' THEN NULL ELSE now() END,version=version+1 WHERE id=$1 AND version=$2 AND state IN ('pending','approved','running')`, v.ID, v.Version)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}
func (s *Service) ClaimWork(ctx context.Context) (WorkOrder, error) {
	return scanWork(s.db.Row(ctx, `UPDATE database_work_orders SET state='running',run_id=gen_random_uuid(),started_at=now(),heartbeat_at=now(),version=version+1 WHERE id=(SELECT id FROM database_work_orders WHERE state='approved' AND NOT cancel_requested ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING `+workFields))
}
func (s *Service) WorkHeartbeat(ctx context.Context, id, run string) error {
	tag, e := s.db.Exec(ctx, `UPDATE database_work_orders SET heartbeat_at=now() WHERE id=$1 AND run_id=$2 AND state='running' AND NOT cancel_requested`, id, run)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Denied
	}
	return e
}
func (s *Service) FinishWork(ctx context.Context, v WorkOrder, state, reason string) error {
	tag, e := s.db.Exec(ctx, `UPDATE database_work_orders SET state=$3,reason=$4,finished_at=now(),version=version+1 WHERE id=$1 AND run_id=$2 AND state='running'`, v.ID, v.RunID, state, reason)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}
