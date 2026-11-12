package identity

import (
	"context"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
)

// NativeSSHPasswordProof revalidates a previously verified local password's
// account/version binding. It never verifies or persists password plaintext.
// Call in the same transaction as Verify on initial authentication, then compare
// version/epoch/factor with the saved proof on every reservation and live lease.
func (s *Service) NativeSSHPasswordProof(ctx context.Context, username string) (p NativeSSHProof, err error) {
	var id string
	if err = s.db.Row(ctx, `SELECT id FROM users WHERE username=$1 AND deleted_at IS NULL`, username).Scan(&id); err != nil {
		return p, fault.Proof
	}
	if err = s.LockUser(ctx, id); err != nil {
		return p, fault.Proof
	}
	cfg, err := system.New(s.db).SecurityConfiguration(ctx)
	if err != nil {
		return p, err
	}
	if cfg.Values.Bool(system.NativePasswordDisabledKey) || cfg.Values.Bool("disable-password-login") {
		return p, fault.Proof
	}
	expired, err := s.PasswordExpired(ctx, id)
	if err != nil {
		return p, err
	}
	if expired {
		return p, fault.Proof
	}
	var cipher []byte
	var required bool
	err = s.db.Row(ctx, `SELECT u.id,u.username,u.nickname,u.type,u.version,i.auth_epoch,u.force_mfa,coalesce(m.secret_cipher,''::bytea) FROM users u CROSS JOIN instance_state i LEFT JOIN mfa_factors m ON m.user_id=u.id WHERE u.id=$1 AND i.initialized_at IS NOT NULL AND u.password_hash LIKE '$argon2id$%'`, id).Scan(&p.User.ID, &p.User.Username, &p.User.Nickname, &p.User.Type, &p.Version, &p.Epoch, &required, &cipher)
	if err != nil {
		return p, fault.Proof
	}
	required = required || cfg.Values.Bool("login-force-totp-enabled") || cfg.Values.Bool("access-require-mfa")
	if required && len(cipher) == 0 {
		return p, fault.Proof
	}
	p.NeedsMFA = required || len(cipher) > 0
	if len(cipher) > 0 {
		p.MFADigest = Digest(string(cipher))
	}
	p.User.Phase = "authenticated"
	return p, nil
}
