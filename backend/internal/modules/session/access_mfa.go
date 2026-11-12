package session

import (
	"context"
	"errors"
	"next-terminal/backend/internal/modules/system"
)

var ErrAccessMFA = errors.New("remote access MFA required")

type AccessMFAStatus struct {
	Required         bool `json:"required"`
	Verified         bool `json:"verified"`
	Enrolled         bool `json:"enrolled"`
	FreshnessSeconds int  `json:"freshnessSeconds"`
}

// Call under the shared transaction. Holding the head prevents a concurrent
// policy change from publishing between proof validation and session creation.
func (s *Service) AccessMFAStatus(ctx context.Context, actor, auth string) (out AccessMFAStatus, err error) {
	var head int64
	if err = s.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR SHARE`).Scan(&head); err != nil {
		return
	}
	v, err := system.New(s.db).SecurityConfiguration(ctx)
	if err != nil {
		return out, err
	}
	out.Required = v.Values.Bool("access-require-mfa")
	out.FreshnessSeconds = v.Values.Int("access-mfa-expires-at") * 60
	err = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM mfa_factors WHERE user_id=$1 AND octet_length(secret_cipher)>0)`, actor).Scan(&out.Enrolled)
	if err != nil {
		return
	}
	if !out.Required {
		out.Verified = true
		return
	}
	err = s.db.Row(ctx, `SELECT EXISTS(
 SELECT 1 FROM session_access_proofs p JOIN users u ON u.id=p.user_id JOIN mfa_factors f ON f.user_id=u.id CROSS JOIN instance_state i
 WHERE p.user_id=$1 AND p.auth_hash=$2 AND u.deleted_at IS NULL AND u.status='enabled' AND p.user_version=u.version AND p.auth_epoch=i.auth_epoch
 AND octet_length(f.secret_cipher)>0 AND p.factor_hash=encode(sha256(f.secret_cipher),'hex')
 AND p.verified_at<=clock_timestamp() AND p.verified_at>clock_timestamp()-make_interval(secs=>CASE WHEN $3::integer=0 THEN 60 ELSE $3 END)
 AND ($3::integer>0 OR p.uses=0))`, actor, auth, out.FreshnessSeconds).Scan(&out.Verified)
	return
}

func (s *Service) consumeAccessMFA(ctx context.Context, actor, auth string) error {
	// Serialize reservations for the same proof, including zero-minute one-use
	// proofs. The advisory key contains no credential or raw token.
	if _, err := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,904098))`, actor+":"+auth); err != nil {
		return err
	}
	status, err := s.AccessMFAStatus(ctx, actor, auth)
	if err != nil {
		return err
	}
	if !status.Required {
		return nil
	}
	if !status.Verified {
		return ErrAccessMFA
	}
	_, err = s.db.Exec(ctx, `UPDATE session_access_proofs SET uses=uses+1 WHERE user_id=$1 AND auth_hash=$2`, actor, auth)
	return err
}
