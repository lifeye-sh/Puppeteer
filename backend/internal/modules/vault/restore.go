package vault

import "context"

func (s *Service) QuarantineRestore(ctx context.Context) error {
	_, e := s.db.Exec(ctx, `UPDATE rotation_targets SET state=CASE WHEN state='pending' THEN 'cancelled' ELSE 'unknown' END,reason='restored_requires_review',updated_at=now() WHERE state IN ('pending','applying','verifying')`)
	return e
}
