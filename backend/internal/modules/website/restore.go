package website

import "context"

func (s *Service) QuarantineRestore(ctx context.Context) error {
	for _, q := range []string{`DELETE FROM website_sessions`, `DELETE FROM website_login_codes`, `UPDATE website_temporary_ips SET revoked_at=now() WHERE revoked_at IS NULL`} {
		if _, e := s.db.Exec(ctx, q); e != nil {
			return e
		}
	}
	return nil
}
