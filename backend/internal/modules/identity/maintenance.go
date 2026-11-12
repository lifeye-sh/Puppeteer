package identity

import "context"

func (s *Service) CleanupFederation(ctx context.Context) error {
	for _, q := range []string{`DELETE FROM contact_challenges WHERE expires_at<=now()`, `DELETE FROM ldap_sync_previews WHERE expires_at<=now()`, `DELETE FROM oidc_authorizations WHERE expires_at<=now()`, `DELETE FROM oidc_login_authorizations WHERE expires_at<=now()`, `DELETE FROM oauth_pending_authorizations WHERE expires_at<=now()`, `DELETE FROM oauth_codes WHERE expires_at<=now()`, `DELETE FROM oauth_tokens WHERE kind='access' AND expires_at<=now()`, `DELETE FROM oauth_token_families WHERE expires_at<=now() OR revoked_at<now()-interval '1 day'`, `DELETE FROM oauth_signing_keys WHERE NOT active AND retire_at<=now()`} {
		if _, e := s.db.Exec(ctx, q); e != nil {
			return e
		}
	}
	return nil
}
