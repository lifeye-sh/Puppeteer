package pki

import "context"

func (s *Service) QuarantineRestore(ctx context.Context) error {
	for _, q := range []string{`UPDATE certificate_orders SET auto_renew=false,status='failed',last_error='restored_requires_review',lease_until=NULL,lease_token=''`, `UPDATE certificates SET revoked_at=now() WHERE kind IN ('client','client_ca') AND revoked_at IS NULL`} {
		if _, e := s.db.Exec(ctx, q); e != nil {
			return e
		}
	}
	return nil
}
