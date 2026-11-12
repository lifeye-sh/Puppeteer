package database

import (
	"context"
	"encoding/json"
	"time"
)

type SQLLog struct {
	WorkOrderID string          `json:"workOrderId"`
	Parameters  json.RawMessage `json:"parameters"`
	Sanitized   bool            `json:"sanitized"`
	ID          string          `json:"id"`
	SessionID   string          `json:"sessionId"`
	User        string          `json:"user"`
	Asset       string          `json:"asset"`
	SQL         string          `json:"sql"`
	Hash        string          `json:"hash"`
	Command     int             `json:"command"`
	State       string          `json:"state"`
	Rows        int64           `json:"rows"`
	Duration    int64           `json:"durationMs"`
	CreatedAt   time.Time       `json:"createdAt"`
	FinishedAt  *time.Time      `json:"finishedAt"`
}

func (s *Service) Logs(ctx context.Context, search, state string, offset, limit int) ([]SQLLog, int, error) {
	where := ` FROM database_sql_logs l LEFT JOIN database_sessions s ON s.id=l.session_id LEFT JOIN database_work_orders w ON w.id=l.work_order_id JOIN users u ON u.id=coalesce(s.user_id,w.requester_id) JOIN resource_registry r ON r.id=coalesce(s.asset_id,w.asset_id) WHERE ($1='' OR l.sql_text ILIKE '%'||$1||'%' OR u.username ILIKE '%'||$1||'%' OR r.display_name ILIKE '%'||$1||'%') AND ($2='' OR l.state=$2)`
	var total int
	if e := s.db.Row(ctx, `SELECT count(*)`+where, search, state).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := s.db.Rows(ctx, `SELECT l.id::text,coalesce(l.session_id::text,''),u.username,r.display_name,l.sql_text,l.sql_sha256,l.command,l.state,l.row_count,l.duration_ms,l.created_at,l.finished_at,coalesce(l.work_order_id::text,''),l.parameter_meta,l.sql_sanitized`+where+` ORDER BY l.created_at DESC,l.id LIMIT $3 OFFSET $4`, search, state, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []SQLLog{}
	for rows.Next() {
		var v SQLLog
		if e = rows.Scan(&v.ID, &v.SessionID, &v.User, &v.Asset, &v.SQL, &v.Hash, &v.Command, &v.State, &v.Rows, &v.Duration, &v.CreatedAt, &v.FinishedAt, &v.WorkOrderID, &v.Parameters, &v.Sanitized); e != nil {
			return nil, 0, e
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}
