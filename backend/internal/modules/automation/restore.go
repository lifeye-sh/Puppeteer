package automation

import "context"

func (s *Service) QuarantineRestore(ctx context.Context) error {
	for _, q := range []string{`UPDATE schedules SET enabled=false,next_run=NULL,pause_reason='restored_requires_review',version=version+1`, `UPDATE scheduled_run_targets SET state='unknown',reason='restored_requires_review',ended_at=now() WHERE state IN ('queued','running')`} {
		if _, e := s.db.Exec(ctx, q); e != nil {
			return e
		}
	}
	return nil
}
