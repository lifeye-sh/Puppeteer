package notification

import (
	"context"
	"encoding/json"
	"fmt"
)

func (s *Service) EnqueueDatabase(ctx context.Context, key, event, order, user string, reviewers []string) error {
	if reviewers == nil {
		reviewers = []string{}
	}
	var state, execution, hash string
	if e := s.db.Row(ctx, `SELECT state,coalesce(run_id::text,''),sql_sha256 FROM database_work_orders WHERE id=$1`, order).Scan(&state, &execution, &hash); e != nil {
		return e
	}
	payload, _ := json.Marshal(map[string]string{"title": "Puppeteer SQL 工单", "text": fmt.Sprintf("SQL 工单 %s：%s；状态 %s。executionId=%s；SQL SHA-256=%s。请登录控制台查看详情。", order, event, state, execution, hash), "executionId": execution, "state": state, "sqlHash": hash})
	_, e := s.db.Exec(ctx, `INSERT INTO notification_events(event_key,event_type,database_order_id,actor_id,reviewer_ids,payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(event_key) DO NOTHING`, key, event, order, user, reviewers, payload)
	return e
}
