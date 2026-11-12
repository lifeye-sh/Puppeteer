package database

import "context"

func (s *Service) QuarantineRestore(ctx context.Context) error {
	for _, query := range []string{
		`UPDATE database_sessions SET ended_at=now(),end_reason='restored_requires_review' WHERE ended_at IS NULL`,
		`UPDATE database_sql_logs SET state='unknown',finished_at=now() WHERE state='running' OR (state='completed' AND work_order_id IN (SELECT id FROM database_work_orders WHERE state='running' AND mode='transaction'))`,
		`UPDATE database_work_orders SET state=CASE WHEN state='running' THEN 'unknown' ELSE 'cancelled' END,cancel_requested=true,reason='恢复后须人工复核，禁止自动重试',finished_at=now(),version=version+1 WHERE state IN ('pending','approved','running')`,
		`UPDATE database_work_order_results SET state='unknown' WHERE state='pending_commit'`,
		`UPDATE resource_registry SET enabled=false,version=version+1 WHERE kind='database' AND enabled`,
	} {
		if _, e := s.db.Exec(ctx, query); e != nil {
			return e
		}
	}
	return nil
}
