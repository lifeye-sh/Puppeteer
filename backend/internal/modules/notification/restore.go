package notification

import "context"

func (s *Service) QuarantineRestore(ctx context.Context) error {
	for _, q := range []string{`UPDATE notification_events SET expanded=true WHERE NOT expanded`, `UPDATE notification_deliveries SET status='skipped',last_error='restored_requires_review',lease_until=NULL,lease_token='' WHERE status IN ('pending','sending')`, `UPDATE notification_channels SET enabled=false,version=version+1`} {
		if _, e := s.db.Exec(ctx, q); e != nil {
			return e
		}
	}
	return nil
}
