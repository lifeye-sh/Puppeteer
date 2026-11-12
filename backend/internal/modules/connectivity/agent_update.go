package connectivity

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
)

func (s *Service) RequestUpdate(ctx context.Context, id, action string) error {
	if action != "check" && action != "install" {
		return fault.Invalid
	}
	tag, e := s.db.Exec(ctx, `UPDATE agent_gateway_configs SET update_request=$2 WHERE id=$1 AND revoked_at IS NULL AND capabilities ? 'managed-update.v1'`, id, action)
	if e == nil && tag.RowsAffected() != 1 {
		e = fault.Invalid
	}
	return e
}
