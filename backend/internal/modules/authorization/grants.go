package authorization

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"time"
)

type Grant struct {
	ID              string     `json:"id"`
	UserID          string     `json:"userId"`
	DepartmentID    string     `json:"departmentId"`
	ResourceID      string     `json:"resourceId"`
	ValidFrom       time.Time  `json:"validFrom"`
	ExpiresAt       *time.Time `json:"expiresAt"`
	Version         int64      `json:"version"`
	StrategyID      string     `json:"strategyId"`
	CommandFilterID string     `json:"commandFilterId"`
	ApprovalID      string     `json:"approvalId"`
}

func (s *Service) Grants(ctx context.Context) ([]Grant, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,COALESCE(user_id::text,''),COALESCE(department_id::text,''),resource_id,valid_from,expires_at,version,coalesce(strategy_id::text,''),coalesce(command_filter_id::text,''),coalesce(approval_id::text,'') FROM grants WHERE revoked_at IS NULL ORDER BY created_at,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var v Grant
		if e = rows.Scan(&v.ID, &v.UserID, &v.DepartmentID, &v.ResourceID, &v.ValidFrom, &v.ExpiresAt, &v.Version, &v.StrategyID, &v.CommandFilterID, &v.ApprovalID); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) SaveGrant(ctx context.Context, v Grant) (string, error) {
	if (v.UserID == "") == (v.DepartmentID == "") {
		return "", fault.Invalid
	}
	if v.ValidFrom.IsZero() {
		v.ValidFrom = time.Now()
	}
	if v.ExpiresAt != nil && !v.ExpiresAt.After(v.ValidFrom) {
		return "", fault.Invalid
	}
	if v.StrategyID != "" || v.CommandFilterID != "" {
		var kind string
		if e := s.db.Row(ctx, `SELECT kind FROM resource_registry WHERE id=$1`, v.ResourceID).Scan(&kind); e != nil {
			return "", e
		}
		// SSH action/command policies cannot be interpreted as SQL policies.
		if kind == "database" {
			return "", fault.Invalid
		}
	}
	if v.ID == "" {
		e := s.db.Row(ctx, `INSERT INTO grants(user_id,department_id,resource_id,valid_from,expires_at,strategy_id,command_filter_id,approval_id) VALUES(NULLIF($1,'')::uuid,NULLIF($2,'')::uuid,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,NULLIF($8,'')::uuid) RETURNING id`, v.UserID, v.DepartmentID, v.ResourceID, v.ValidFrom, v.ExpiresAt, v.StrategyID, v.CommandFilterID, v.ApprovalID).Scan(&v.ID)
		return v.ID, e
	}
	tag, e := s.db.Exec(ctx, `UPDATE grants SET user_id=NULLIF($2,'')::uuid,department_id=NULLIF($3,'')::uuid,resource_id=$4,valid_from=$5,expires_at=$6,version=version+1,strategy_id=NULLIF($8,'')::uuid,command_filter_id=NULLIF($9,'')::uuid WHERE id=$1 AND version=$7 AND revoked_at IS NULL AND approval_id IS NULL`, v.ID, v.UserID, v.DepartmentID, v.ResourceID, v.ValidFrom, v.ExpiresAt, v.Version, v.StrategyID, v.CommandFilterID)
	if e == nil && tag.RowsAffected() != 1 {
		return "", fault.Conflict
	}
	return v.ID, e
}
func (s *Service) RevokeGrant(ctx context.Context, id string) error {
	tag, e := s.db.Exec(ctx, `UPDATE grants SET revoked_at=now(),version=version+1 WHERE id=$1 AND revoked_at IS NULL`, id)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}

// Identity facts are supplied by M01; resource expansion is performed by M03.
func (s *Service) Targets(ctx context.Context, user string, departments []string) ([]string, error) {
	if departments == nil {
		departments = []string{}
	}
	rows, e := s.db.Rows(ctx, `SELECT DISTINCT resource_id::text FROM grants WHERE revoked_at IS NULL AND valid_from<=now() AND (expires_at IS NULL OR expires_at>now()) AND (user_id=$1 OR department_id=ANY($2::uuid[]))`, user, departments)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
