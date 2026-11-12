package session

import "context"

func (s *Service) QuarantineRestore(ctx context.Context) error {
	if _, e := s.db.Exec(ctx, `UPDATE native_forward_streams SET state='interrupted',ended_at=now() WHERE ended_at IS NULL`); e != nil {
		return e
	}
	for _, q := range []string{`DELETE FROM native_rdp_tickets`, `UPDATE sessions SET ticket_hash='',auth_hash='',state=CASE WHEN closed_at IS NULL THEN 'interrupted' ELSE state END,closed_at=COALESCE(closed_at,now()),close_reason=CASE WHEN closed_at IS NULL THEN 'restored_requires_review' ELSE close_reason END`, `UPDATE session_shares SET revoked_at=now() WHERE revoked_at IS NULL`, `DELETE FROM session_observers`} {
		if _, e := s.db.Exec(ctx, q); e != nil {
			return e
		}
	}
	return nil
}
