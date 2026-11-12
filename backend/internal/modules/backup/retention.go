package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Mark before deleting, retry deleting entries after a crash, retain the newest
// archive and skip active download locks or unexpired export tickets.
func (s *Service) Prune(ctx context.Context, dir string) error {
	cfg, e := s.Config(ctx)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `WITH candidates AS (SELECT id FROM backup_files WHERE state='available' AND created_at<now()-$1::int*interval '1 day' AND id<>(SELECT id FROM backup_files WHERE state='available' ORDER BY created_at DESC LIMIT 1) AND EXISTS(SELECT 1 FROM jobs j WHERE j.id=backup_files.id AND j.state NOT IN ('queued','running')) AND id IS DISTINCT FROM (SELECT f.id FROM backup_files f JOIN jobs j ON j.id=f.id WHERE f.state='available' AND j.state='succeeded' ORDER BY f.created_at DESC LIMIT 1) AND NOT EXISTS(SELECT 1 FROM backup_export_tickets t WHERE t.file_id=backup_files.id AND t.expires_at>now()) FOR UPDATE SKIP LOCKED) UPDATE backup_files SET state='deleting' WHERE id IN (SELECT id FROM candidates)`, cfg.RetentionDays)
	if e != nil {
		return e
	}
	rows, e := s.db.Rows(ctx, `SELECT id::text,storage_key FROM backup_files WHERE state='deleting'`)
	if e != nil {
		return e
	}
	type item struct{ id, key string }
	items := []item{}
	for rows.Next() {
		var i item
		if e = rows.Scan(&i.id, &i.key); e != nil {
			rows.Close()
			return e
		}
		items = append(items, i)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, i := range items {
		if i.key != i.id+".ntbackup.age" || filepath.Base(i.key) != i.key {
			return errors.New("invalid backup storage key")
		}
		if e = os.Remove(filepath.Join(dir, i.key)); e != nil && !os.IsNotExist(e) {
			return e
		}
		if _, e = s.db.Exec(ctx, `UPDATE backup_files SET state='missing' WHERE id=$1 AND state='deleting'`, i.id); e != nil {
			return e
		}
	}
	_, e = s.db.Exec(ctx, `DELETE FROM backup_export_tickets WHERE expires_at<now()`)
	return e
}
