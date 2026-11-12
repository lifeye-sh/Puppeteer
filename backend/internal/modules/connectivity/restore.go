package connectivity

import "context"

func (s *Service) QuarantineRestore(ctx context.Context) error {
	for _, q := range []string{`UPDATE agent_registration_tokens SET revoked_at=now() WHERE revoked_at IS NULL`, `UPDATE agent_gateway_configs SET revoked_at=now(),connection_epoch=connection_epoch+1 WHERE revoked_at IS NULL`} {
		if _, e := s.db.Exec(ctx, q); e != nil {
			return e
		}
	}
	return nil
}
