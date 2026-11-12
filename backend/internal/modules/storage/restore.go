package storage

import "context"

func (s *Service) QuarantineRestore(ctx context.Context) error {
	for _, q := range []string{
		`UPDATE user_storage u SET used_bytes=u.used_bytes-t.total FROM (SELECT space_id,sum(size) total FROM storage_upload_tasks WHERE state='uploading' GROUP BY space_id) t WHERE u.user_id=t.space_id`,
		`UPDATE storage_upload_tasks SET state='cancelled' WHERE state='uploading'`,
		`DELETE FROM storage_upload_parts`,
	} {
		if _, e := s.db.Exec(ctx, q); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) MarkPayloadUnavailable(ctx context.Context) error {
	_, e := s.db.Exec(ctx, `UPDATE storage_nodes SET payload_unavailable=true WHERE NOT directory AND size>0`)
	return e
}
