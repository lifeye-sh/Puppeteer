package approval

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"slices"
	"time"
)

type Service struct{ db *postgres.DB }

func New(db *postgres.DB) *Service { return &Service{db} }

type Scope struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	RequesterIDs  []string `json:"requesterIds"`
	DepartmentIDs []string `json:"departmentIds"`
	ResourceIDs   []string `json:"resourceIds"`
	ReviewerIDs   []string `json:"reviewerIds"`
	MaxMinutes    int      `json:"maxMinutes"`
	Enabled       bool     `json:"enabled"`
	Version       int64    `json:"version"`
}
type Request struct {
	ID             string     `json:"id"`
	ActorID        string     `json:"actorId"`
	ResourceID     string     `json:"resourceId"`
	ScopeID        string     `json:"scopeId"`
	Reason         string     `json:"reason"`
	Duration       int        `json:"durationMinutes"`
	RequestedStart time.Time  `json:"requestedStart"`
	Status         string     `json:"status"`
	ReviewerID     string     `json:"reviewerId"`
	DecisionReason string     `json:"decisionReason"`
	GrantID        string     `json:"grantId"`
	StartsAt       *time.Time `json:"startsAt"`
	ExpiresAt      *time.Time `json:"expiresAt"`
	Version        int64      `json:"version"`
	CreatedAt      time.Time  `json:"createdAt"`
	DecidedAt      *time.Time `json:"decidedAt"`
}

const scopeCols = "id::text,name,requester_ids,department_ids,resource_ids,reviewer_ids,max_minutes,enabled,version"

func (s *Service) Scopes(ctx context.Context) ([]Scope, error) {
	rows, e := s.db.Rows(ctx, "SELECT "+scopeCols+" FROM approval_scopes ORDER BY name,id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Scope{}
	for rows.Next() {
		var v Scope
		if e = rows.Scan(&v.ID, &v.Name, &v.RequesterIDs, &v.DepartmentIDs, &v.ResourceIDs, &v.ReviewerIDs, &v.MaxMinutes, &v.Enabled, &v.Version); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) SaveScope(ctx context.Context, v Scope) (string, error) {
	if v.Name == "" || len(v.Name) > 100 || v.MaxMinutes < 1 || v.MaxMinutes > 10080 || len(v.ResourceIDs) == 0 || len(v.ReviewerIDs) == 0 || len(v.RequesterIDs)+len(v.DepartmentIDs) == 0 || len(v.ResourceIDs)+len(v.ReviewerIDs)+len(v.RequesterIDs)+len(v.DepartmentIDs) > 1000 {
		return "", fault.Invalid
	}
	if v.RequesterIDs == nil {
		v.RequesterIDs = []string{}
	}
	if v.DepartmentIDs == nil {
		v.DepartmentIDs = []string{}
	}
	if v.ID == "" {
		e := s.db.Row(ctx, "INSERT INTO approval_scopes(name,requester_ids,department_ids,resource_ids,reviewer_ids,max_minutes,enabled) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text", v.Name, v.RequesterIDs, v.DepartmentIDs, v.ResourceIDs, v.ReviewerIDs, v.MaxMinutes, v.Enabled).Scan(&v.ID)
		return v.ID, e
	}
	tag, e := s.db.Exec(ctx, "UPDATE approval_scopes SET name=$2,requester_ids=$3,department_ids=$4,resource_ids=$5,reviewer_ids=$6,max_minutes=$7,enabled=$8,version=version+1 WHERE id=$1 AND version=$9", v.ID, v.Name, v.RequesterIDs, v.DepartmentIDs, v.ResourceIDs, v.ReviewerIDs, v.MaxMinutes, v.Enabled, v.Version)
	if e == nil && tag.RowsAffected() != 1 {
		e = fault.Conflict
	}
	return v.ID, e
}
func overlap(a, b []string) bool {
	for _, v := range a {
		if slices.Contains(b, v) {
			return true
		}
	}
	return false
}
func Matches(s Scope, user string, deps, resources []string) bool {
	return s.Enabled && (slices.Contains(s.RequesterIDs, user) || overlap(s.DepartmentIDs, deps)) && overlap(s.ResourceIDs, resources)
}
func CanReview(s Scope, user string) bool { return s.Enabled && slices.Contains(s.ReviewerIDs, user) }

func (s *Service) Scope(ctx context.Context, id string, lock bool) (v Scope, e error) {
	q := "SELECT " + scopeCols + " FROM approval_scopes WHERE id=$1"
	if lock {
		q += " FOR SHARE"
	}
	e = s.db.Row(ctx, q, id).Scan(&v.ID, &v.Name, &v.RequesterIDs, &v.DepartmentIDs, &v.ResourceIDs, &v.ReviewerIDs, &v.MaxMinutes, &v.Enabled, &v.Version)
	return
}

const requestCols = "id::text,actor_id::text,resource_id::text,scope_id::text,reason,duration_minutes,requested_start,status,coalesce(reviewer_id::text,''),decision_reason,coalesce(grant_id::text,''),starts_at,expires_at,version,created_at,decided_at"

func (s *Service) Get(ctx context.Context, id string, lock bool) (v Request, e error) {
	q := "SELECT " + requestCols + " FROM access_requests WHERE id=$1"
	if lock {
		q += " FOR UPDATE"
	}
	e = s.db.Row(ctx, q, id).Scan(&v.ID, &v.ActorID, &v.ResourceID, &v.ScopeID, &v.Reason, &v.Duration, &v.RequestedStart, &v.Status, &v.ReviewerID, &v.DecisionReason, &v.GrantID, &v.StartsAt, &v.ExpiresAt, &v.Version, &v.CreatedAt, &v.DecidedAt)
	return
}
func (s *Service) Submit(ctx context.Context, v Request) (string, error) {
	if len(v.Reason) == 0 || len(v.Reason) > 2000 || v.Duration < 1 || v.Duration > 10080 {
		return "", fault.Invalid
	}
	if v.RequestedStart.IsZero() {
		v.RequestedStart = time.Now()
	}
	if v.RequestedStart.Before(time.Now().Add(-time.Minute)) || v.RequestedStart.After(time.Now().Add(7*24*time.Hour)) {
		return "", fault.Invalid
	}
	e := s.db.Row(ctx, "INSERT INTO access_requests(actor_id,resource_id,scope_id,reason,duration_minutes,requested_start) VALUES($1,$2,$3,$4,$5,$6) RETURNING id::text", v.ActorID, v.ResourceID, v.ScopeID, v.Reason, v.Duration, v.RequestedStart).Scan(&v.ID)
	return v.ID, e
}
func (s *Service) Decide(ctx context.Context, v Request, status, reviewer, reason, grant string, start, end *time.Time) error {
	if !((v.Status == "pending" && (status == "approved" || status == "rejected" || status == "cancelled")) || (v.Status == "approved" && (status == "expired" || status == "cancelled"))) {
		return fault.Conflict
	}
	if status != "approved" && status != "rejected" && status != "cancelled" && status != "expired" || len(reason) > 2000 {
		return fault.Invalid
	}
	tag, e := s.db.Exec(ctx, `UPDATE access_requests SET status=$3,reviewer_id=nullif($4,'')::uuid,decision_reason=$5,grant_id=nullif($6,'')::uuid,starts_at=$7,expires_at=$8,version=version+1,decided_at=now() WHERE id=$1 AND version=$2`, v.ID, v.Version, status, reviewer, reason, grant, start, end)
	if e == nil && tag.RowsAffected() != 1 {
		e = fault.Conflict
	}
	return e
}
func (s *Service) List(ctx context.Context, user string, admin bool, page int) ([]Request, int64, error) {
	const where = ` FROM access_requests r JOIN approval_scopes s ON s.id=r.scope_id WHERE r.actor_id=$1 OR $2 OR $1::uuid=ANY(s.reviewer_ids)`
	var total int64
	if e := s.db.Row(ctx, "SELECT count(*)"+where, user, admin).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := s.db.Rows(ctx, "SELECT r.id::text"+where+" ORDER BY r.created_at DESC LIMIT 50 OFFSET $3", user, admin, (page-1)*50)
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
	out := []Request{}
	for _, id := range ids {
		v, e := s.Get(ctx, id, false)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, v)
	}
	return out, total, nil
}
func (s *Service) Expiring(ctx context.Context) ([]string, error) {
	rows, e := s.db.Rows(ctx, "SELECT id::text FROM access_requests WHERE status='approved' AND expires_at<=now() ORDER BY expires_at LIMIT 100")
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
