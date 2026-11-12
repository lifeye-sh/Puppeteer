package database

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/platform/fault"
	"time"
)

type WorkResult struct {
	Index    int             `json:"index"`
	State    string          `json:"state"`
	Affected uint64          `json:"affectedRows"`
	Data     json.RawMessage `json:"data"`
	Duration int64           `json:"durationMs"`
}

func (s *Service) SaveWorkResult(ctx context.Context, order string, index int, state string, affected uint64, data []byte, duration time.Duration) error {
	if len(data) > 4<<20 {
		return fault.Invalid
	}
	if len(data) == 0 {
		data = []byte(`{}`)
	}
	_, e := s.db.Exec(ctx, `INSERT INTO database_work_order_results(order_id,statement_index,state,affected_rows,result,duration_ms) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(order_id,statement_index) DO UPDATE SET state=excluded.state,affected_rows=excluded.affected_rows,result=excluded.result,duration_ms=excluded.duration_ms`, order, index, state, affected, data, duration.Milliseconds())
	return e
}
func (s *Service) FinalizeWorkResults(ctx context.Context, order, state string) error {
	_, e := s.db.Exec(ctx, `UPDATE database_work_order_results SET state=$2 WHERE order_id=$1 AND state='pending_commit'`, order, state)
	return e
}
func (s *Service) WorkResults(ctx context.Context, order string, offset, limit int) ([]WorkResult, error) {
	rows, e := s.db.Rows(ctx, `SELECT statement_index,state,affected_rows,result,duration_ms FROM database_work_order_results WHERE order_id=$1 ORDER BY statement_index LIMIT $2 OFFSET $3`, order, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []WorkResult{}
	for rows.Next() {
		var v WorkResult
		if e = rows.Scan(&v.Index, &v.State, &v.Affected, &v.Data, &v.Duration); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
