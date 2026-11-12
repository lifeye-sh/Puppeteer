package session

import "context"

// Keep the technical session reference for independent file/share audit FKs.
// Purged sessions are excluded from the access-history and session-detail APIs.
func (s *Service) RetentionCandidates(ctx context.Context, days int) ([]string, error) {
	// Legacy helper; callers must hold the global retention lock before selecting.
	if days < 1 {
		return []string{}, nil
	}
	rows, e := s.db.Rows(ctx, `SELECT id FROM sessions s WHERE audit_purged_at IS NULL AND closed_at<now()-($1*interval '1 day') AND NOT EXISTS(SELECT 1 FROM audit_holds h WHERE h.category='access' AND h.record_id=s.id) ORDER BY closed_at,id LIMIT 25 FOR UPDATE SKIP LOCKED`, days)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (s *Service) MarkAuditPurged(ctx context.Context, id string) error {
	_, e := s.db.Exec(ctx, `UPDATE sessions SET audit_purged_at=now(),ticket_hash='',auth_hash='',client_ip='',close_reason='' WHERE id=$1 AND closed_at IS NOT NULL`, id)
	return e
}
