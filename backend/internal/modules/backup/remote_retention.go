package backup

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/platform/secretbox"
)

func (s *Service) PruneRemote(ctx context.Context, key []byte) error {
	_, e := s.db.Exec(ctx, `UPDATE backup_copies c SET state='deleting' WHERE state IN ('verified','failed','pending','copying') AND reason<>'restored_requires_review' AND EXISTS(SELECT 1 FROM backup_files f WHERE f.id=c.task_id AND f.state='missing')`)
	if e != nil {
		return e
	}
	rows, e := s.db.Rows(ctx, `SELECT task_id::text,destination_id::text,config_snapshot,secret,remote_key FROM backup_copies WHERE state='deleting'`)
	if e != nil {
		return e
	}
	type item struct {
		task, id, remote string
		raw, secret      []byte
	}
	items := []item{}
	for rows.Next() {
		var v item
		if e = rows.Scan(&v.task, &v.id, &v.raw, &v.secret, &v.remote); e != nil {
			rows.Close()
			return e
		}
		items = append(items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range items {
		var d Destination
		if e = json.Unmarshal(v.raw, &d); e != nil {
			return e
		}
		plain, e := secretbox.Open(key, v.secret, "backup-destination:"+v.id)
		if e != nil {
			return e
		}
		d.Secret = string(plain)
		clear(plain)
		e = RemoveRemote(ctx, d, v.remote)
		if e == nil {
			e = RemoveRemote(ctx, d, v.remote+".partial")
		}
		d.Secret = ""
		if e != nil {
			return e
		}
		if _, e = s.db.Exec(ctx, `UPDATE backup_copies SET state='deleted' WHERE task_id=$1 AND destination_id=$2 AND state='deleting'`, v.task, v.id); e != nil {
			return e
		}
	}
	return nil
}
