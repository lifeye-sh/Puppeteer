package audit

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/platform/postgres"
	"time"
)

type Service struct{ db *postgres.DB }

func New(db *postgres.DB) *Service { return &Service{db} }
func (s *Service) Detail(ctx context.Context, id, action, requestID, ip string, details map[string]string) error {
	raw, e := json.Marshal(details)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO operation_logs(actor_id,action,outcome,request_id,source_ip,details) VALUES($1,$2,'succeeded',$3,$4,$5)`, actor(id), action, requestID, ip, raw)
	return e
}
func actor(id string) any {
	if id == "" {
		return nil
	}
	return id
}
func (s *Service) Operation(ctx context.Context, id, action, outcome, requestID, ip string) error {
	_, e := s.db.Exec(ctx, `INSERT INTO operation_logs(actor_id,action,outcome,request_id,source_ip) VALUES($1,$2,$3,$4,$5)`, actor(id), action, outcome, requestID, ip)
	return e
}
func (s *Service) Login(ctx context.Context, id, outcome, requestID, ip string) error {
	_, e := s.db.Exec(ctx, `INSERT INTO login_logs(actor_id,outcome,request_id,source_ip) VALUES($1,$2,$3,$4)`, actor(id), outcome, requestID, ip)
	return e
}

type Event struct {
	ID        string            `json:"id"`
	Action    string            `json:"action"`
	Outcome   string            `json:"outcome"`
	RequestID string            `json:"requestId"`
	CreatedAt time.Time         `json:"createdAt"`
	Actor     string            `json:"actor"`
	Details   map[string]string `json:"details"`
}

func (s *Service) Recent(ctx context.Context) ([]Event, error) {
	rows, e := s.db.Rows(ctx, `SELECT a.id,action,outcome,request_id,a.created_at,COALESCE(a.actor_id::text,''),details FROM (SELECT id,actor_id,action,outcome,request_id,created_at,details FROM operation_logs UNION ALL SELECT id,actor_id,'login' AS action,outcome,request_id,created_at,'{}'::jsonb FROM login_logs) a ORDER BY a.created_at DESC,a.id DESC LIMIT 20`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []Event{}
	for rows.Next() {
		var v Event
		if e = rows.Scan(&v.ID, &v.Action, &v.Outcome, &v.RequestID, &v.CreatedAt, &v.Actor, &v.Details); e != nil {
			return nil, e
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
