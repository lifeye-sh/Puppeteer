package connectivity

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
)

func (s *Service) RequestRotation(ctx context.Context, id string) error {
	tag, e := s.db.Exec(ctx, `UPDATE agent_gateway_configs SET rotation_requested=true WHERE id=$1 AND revoked_at IS NULL AND capabilities ? 'key-rotation.v1'`, id)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Invalid
	}
	return e
}

// Client persists its proposed key before sending. Retrying a lost response is idempotent.
func (s *Service) RotateDevice(ctx context.Context, old, next string) (string, error) {
	if len(old) != 64 || len(next) != 64 || old == next {
		return "", fault.Invalid
	}
	var id string
	e := s.db.Row(ctx, `UPDATE agent_gateway_configs SET previous_credential_hash=CASE WHEN credential_hash=$1 THEN credential_hash ELSE previous_credential_hash END,previous_expires_at=CASE WHEN credential_hash=$1 THEN now()+interval '5 minutes' ELSE previous_expires_at END,credential_version=credential_version+CASE WHEN credential_hash=$1 THEN 1 ELSE 0 END,credential_hash=$2,rotation_requested=false WHERE revoked_at IS NULL AND (credential_hash=$1 OR (previous_credential_hash=$1 AND previous_expires_at>now() AND credential_hash=$2)) RETURNING id`, deviceDigest(old), deviceDigest(next)).Scan(&id)
	if e != nil {
		return "", fault.Denied
	}
	return id, nil
}
