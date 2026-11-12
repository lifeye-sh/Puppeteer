package identity

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"regexp"
)

var accessAuthHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

// RecordAccessMFA is only for callers that just consumed TOTP in this
// transaction, or completed a verified SSH handshake and rechecked its factor.
// It never infers access proof from a login timestamp or a recovery code.
func (s *Service) RecordAccessMFA(ctx context.Context, id, auth string) error {
	if !s.db.InTransaction(ctx) || !accessAuthHash.MatchString(auth) {
		return fault.Invalid
	}
	if err := s.LockUser(ctx, id); err != nil {
		return err
	}
	var head int64
	if err := s.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR SHARE`).Scan(&head); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `DELETE FROM session_access_proofs WHERE verified_at<clock_timestamp()-interval '1 day'`)
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `INSERT INTO session_access_proofs(user_id,auth_hash,factor_hash,user_version,auth_epoch)
 SELECT u.id,$2,encode(sha256(f.secret_cipher),'hex'),u.version,i.auth_epoch FROM users u JOIN mfa_factors f ON f.user_id=u.id CROSS JOIN instance_state i
 WHERE u.id=$1 AND u.deleted_at IS NULL AND u.status='enabled' AND octet_length(f.secret_cipher)>0
 ON CONFLICT(user_id,auth_hash) DO UPDATE SET factor_hash=excluded.factor_hash,user_version=excluded.user_version,auth_epoch=excluded.auth_epoch,verified_at=clock_timestamp(),uses=0`, id, auth)
	if err == nil && tag.RowsAffected() != 1 {
		return fault.Proof
	}
	return err
}
