package resource

import "context"

func (s *Service) QuarantineRestore(ctx context.Context) error {
	_, e := s.db.Exec(ctx, `UPDATE resource_registry SET enabled=false WHERE kind='website'`)
	return e
}
