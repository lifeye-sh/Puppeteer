package audit

import "context"

func (s *Service) QuarantineRestore(ctx context.Context) error {
	for _, q := range []string{
		`DELETE FROM recording_exports`,
		`UPDATE access_logs SET state='interrupted',reason='restored_requires_review',ended_at=now() WHERE ended_at IS NULL`,
		`UPDATE recordings SET state='interrupted',ended_at=now() WHERE state='writing'`,
		`UPDATE session_commands SET state='interrupted',ended_at=now(),note='restored_requires_review' WHERE state='running'`,
	} {
		if _, e := s.db.Exec(ctx, q); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) MarkPayloadUnavailable(ctx context.Context) error {
	_, e := s.db.Exec(ctx, `UPDATE recordings SET state='unavailable',events=0,bytes=0,sha256='',integrity_state='unsealed'`)
	return e
}
