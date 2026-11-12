package identity

import (
	"context"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
)

type NativeSSHProof struct {
	User           User
	KeyID          string
	Version, Epoch int64
	MFADigest      string
	NeedsMFA       bool
}

// NativeSSHKey only checks a registered public key. SSH verifies possession of
// the private key; the application must additionally complete required TOTP.
// Call within a transaction, including when renewing a live session lease.
func (s *Service) NativeSSHKey(ctx context.Context, username, canonical string) (p NativeSSHProof, e error) {
	var id string
	if e = s.db.Row(ctx, `SELECT id FROM users WHERE username=$1 AND deleted_at IS NULL`, username).Scan(&id); e != nil {
		return p, fault.Proof
	}
	if e = s.LockUser(ctx, id); e != nil {
		return p, fault.Proof
	}
	var required bool
	var cipher []byte
	e = s.db.Row(ctx, `SELECT u.id,u.username,u.nickname,u.type,u.version,i.auth_epoch,k.id,u.force_mfa,coalesce(m.secret_cipher,''::bytea) FROM users u JOIN personal_ssh_keys k ON k.user_id=u.id CROSS JOIN instance_state i LEFT JOIN mfa_factors m ON m.user_id=u.id WHERE u.id=$1 AND k.public_key=$2 AND k.revoked_at IS NULL AND k.expires_at>clock_timestamp() AND i.initialized_at IS NOT NULL`, id, canonical).Scan(&p.User.ID, &p.User.Username, &p.User.Nickname, &p.User.Type, &p.Version, &p.Epoch, &p.KeyID, &required, &cipher)
	if e != nil {
		return p, fault.Proof
	}
	_, globallyRequired, err := s.MFAStatus(ctx, id)
	if err != nil {
		return p, err
	}
	configuration, err := system.New(s.db).SecurityConfiguration(ctx)
	if err != nil {
		return p, err
	}
	required = required || globallyRequired || configuration.Values.Bool("access-require-mfa")
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

func (p NativeSSHProof) Same(other NativeSSHProof) bool {
	return p.User.ID == other.User.ID && p.KeyID == other.KeyID && p.Version == other.Version && p.Epoch == other.Epoch && p.MFADigest == other.MFADigest && p.NeedsMFA == other.NeedsMFA
}
