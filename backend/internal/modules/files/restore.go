package files

import "context"

func (s *Service) QuarantineRestore(ctx context.Context) error {
	for _, q := range []string{
		`UPDATE upload_tasks SET state='interrupted',cancel_requested=true,cleanup_needed=false,message='restored_requires_review' WHERE state IN ('receiving','transferring','committing')`,
		`DELETE FROM upload_chunks`,
	} {
		if _, e := s.db.Exec(ctx, q); e != nil {
			return e
		}
	}
	return nil
}
