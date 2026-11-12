package storage

import (
	"bytes"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"syscall"
	"time"
)

type Upload struct {
	ID       string    `json:"id"`
	Actor    string    `json:"-"`
	Space    string    `json:"spaceId"`
	Parent   int64     `json:"parentId"`
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Received int64     `json:"received"`
	State    string    `json:"state"`
	Node     int64     `json:"nodeId"`
	Expires  time.Time `json:"expiresAt"`
}

const uploadCols = "id::text,actor_id::text,space_id::text,parent_id,name,size,received,state,coalesce(node_id,0),expires_at"

func scanUpload(row pgx.Row) (u Upload, e error) {
	e = row.Scan(&u.ID, &u.Actor, &u.Space, &u.Parent, &u.Name, &u.Size, &u.Received, &u.State, &u.Node, &u.Expires)
	if errors.Is(e, pgx.ErrNoRows) {
		e = syscall.ENOENT
	}
	return
}
func (s *Service) Upload(ctx context.Context, id string) (Upload, error) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	if !ok {
		return Upload{}, syscall.EACCES
	}
	u, e := scanUpload(s.db.Row(ctx, "SELECT "+uploadCols+" FROM storage_upload_tasks WHERE id=$1 AND actor_id=$2", id, a.User))
	if e != nil {
		return u, e
	}
	if e = s.authorize(ctx, u.Space, "list"); e != nil {
		return u, e
	}
	return u, nil
}
func (s *Service) Uploads(ctx context.Context, space string) ([]Upload, error) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	if !ok {
		return nil, syscall.EACCES
	}
	if e := s.authorize(ctx, space, "list"); e != nil {
		return nil, e
	}
	rows, e := s.db.Rows(ctx, "SELECT "+uploadCols+" FROM storage_upload_tasks WHERE actor_id=$1 AND space_id=$2 AND state='uploading' ORDER BY created_at DESC LIMIT 100", a.User, space)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Upload{}
	for rows.Next() {
		u, e := scanUpload(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Service) StartUpload(ctx context.Context, space string, parent int64, name string, size int64) (out Upload, err error) {
	if err = s.authorizeAction(ctx, space, "createFile"); err != nil {
		return
	}
	a, ok := ctx.Value(actorKey{}).(Actor)
	if !ok {
		return out, syscall.EACCES
	}
	if !validName(name) || size < 0 || size > 1<<30 {
		return out, syscall.EINVAL
	}
	err = s.db.Within(ctx, func(c context.Context) error {
		usage, e := s.lock(c, space)
		if e != nil {
			return e
		}
		p, e := s.Get(c, space, parent)
		if e != nil {
			return e
		}
		if !p.Directory {
			return syscall.ENOTDIR
		}
		if size > usage.Quota-usage.Used {
			return syscall.EDQUOT
		}
		if _, e = s.Lookup(c, space, parent, name); e == nil {
			return syscall.EEXIST
		} else if !errors.Is(e, syscall.ENOENT) {
			return e
		}
		var count int
		if e = s.db.Row(c, "SELECT (SELECT count(*) FROM storage_nodes WHERE user_id=$1)+(SELECT count(*) FROM storage_upload_tasks WHERE space_id=$1 AND state='uploading')", space).Scan(&count); e != nil {
			return e
		}
		if count >= 10000 {
			return syscall.ENOSPC
		}
		var active bool
		if e = s.db.Row(c, "SELECT EXISTS(SELECT 1 FROM storage_upload_tasks WHERE space_id=$1 AND parent_id=$2 AND name=$3 AND state='uploading')", space, parent, name).Scan(&active); e != nil {
			return e
		}
		if active {
			return syscall.EEXIST
		}
		out, e = scanUpload(s.db.Row(c, "INSERT INTO storage_upload_tasks(actor_id,space_id,parent_id,name,size) VALUES($1,$2,$3,$4,$5) RETURNING "+uploadCols, a.User, space, parent, name, size))
		if e != nil {
			return e
		}
		if _, e = s.db.Exec(c, "UPDATE user_storage SET used_bytes=used_bytes+$2 WHERE user_id=$1", space, size); e != nil {
			return e
		}
		return s.event(c, space, 0, "upload_start", size, name)
	})
	return
}
func (s *Service) lockUpload(ctx context.Context, id string) (Upload, error) {
	u, e := s.Upload(ctx, id)
	if e != nil {
		return u, e
	}
	if _, e = s.lock(ctx, u.Space); e != nil {
		return u, e
	}
	u, e = scanUpload(s.db.Row(ctx, "SELECT "+uploadCols+" FROM storage_upload_tasks WHERE id=$1 FOR UPDATE", id))
	if e != nil {
		return u, e
	}
	if u.State == "uploading" && time.Now().After(u.Expires) {
		return u, syscall.ESTALE
	}
	return u, nil
}

// Only consecutive aligned chunks are accepted. Repeating an acknowledged
// chunk is harmless only when its bytes exactly match the committed chunk.
func (s *Service) UploadPart(ctx context.Context, id string, offset int64, data []byte) error {
	if offset < 0 || offset%ChunkSize != 0 || len(data) == 0 || len(data) > 1<<20 {
		return syscall.EINVAL
	}
	return s.db.Within(ctx, func(c context.Context) error {
		u, e := s.lockUpload(c, id)
		if e != nil {
			return e
		}
		if u.State != "uploading" {
			return syscall.ESTALE
		}
		end := offset + int64(len(data))
		if end > u.Size || offset > u.Received || (end < u.Size && len(data)%ChunkSize != 0) {
			return syscall.EINVAL
		}
		if offset < u.Received {
			if end > u.Received {
				return syscall.ESTALE
			}
			for pos := 0; pos < len(data); pos += ChunkSize {
				var old []byte
				if e = s.db.Row(c, "SELECT data FROM storage_upload_parts WHERE upload_id=$1 AND chunk_index=$2", id, offset/ChunkSize+int64(pos/ChunkSize)).Scan(&old); e != nil {
					return e
				}
				if !bytes.Equal(old, data[pos:min(pos+ChunkSize, len(data))]) {
					return syscall.ESTALE
				}
			}
			return nil
		}
		for pos := 0; pos < len(data); pos += ChunkSize {
			if _, e = s.db.Exec(c, "INSERT INTO storage_upload_parts VALUES($1,$2,$3)", id, offset/ChunkSize+int64(pos/ChunkSize), data[pos:min(pos+ChunkSize, len(data))]); e != nil {
				return e
			}
		}
		_, e = s.db.Exec(c, "UPDATE storage_upload_tasks SET received=$2 WHERE id=$1", id, end)
		return e
	})
}
func (s *Service) FinishUpload(ctx context.Context, id string) (out Upload, err error) {
	err = s.db.Within(ctx, func(c context.Context) error {
		u, e := s.lockUpload(c, id)
		if e != nil {
			return e
		}
		if u.State == "complete" {
			out = u
			return nil
		}
		if e = s.authorizeAction(c, u.Space, "createFile"); e != nil {
			return e
		}
		if u.State != "uploading" || u.Received != u.Size {
			return syscall.ESTALE
		}
		if _, e = s.Lookup(c, u.Space, u.Parent, u.Name); e == nil {
			return syscall.EEXIST
		} else if !errors.Is(e, syscall.ENOENT) {
			return e
		}
		n, e := scan(s.db.Row(c, "INSERT INTO storage_nodes(user_id,parent_id,name,directory,size) VALUES($1,$2,$3,false,$4) RETURNING "+cols, u.Space, u.Parent, u.Name, u.Size))
		if e != nil {
			return e
		}
		if _, e = s.db.Exec(c, "INSERT INTO storage_chunks SELECT $2,chunk_index,data FROM storage_upload_parts WHERE upload_id=$1", id, n.ID); e != nil {
			return e
		}
		if _, e = s.db.Exec(c, "DELETE FROM storage_upload_parts WHERE upload_id=$1", id); e != nil {
			return e
		}
		if _, e = s.db.Exec(c, "UPDATE storage_upload_tasks SET state='complete',node_id=$2 WHERE id=$1", id, n.ID); e != nil {
			return e
		}
		u.State = "complete"
		u.Node = n.ID
		out = u
		return s.event(c, u.Space, n.ID, "upload_complete", u.Size)
	})
	return
}
func (s *Service) cancelUpload(ctx context.Context, id, actor, state string) error {
	return s.db.Within(ctx, func(c context.Context) error {
		u, e := scanUpload(s.db.Row(c, "SELECT "+uploadCols+" FROM storage_upload_tasks WHERE id=$1 AND ($2='' OR actor_id::text=$2)", id, actor))
		if e != nil {
			return e
		}
		// Cancellation remains possible after ACL revocation. It only releases this
		// actor's staging data; no final file can be modified through this path.
		var ignored int64
		if e = s.db.Row(c, "SELECT used_bytes FROM user_storage WHERE user_id=$1 FOR UPDATE", u.Space).Scan(&ignored); e != nil {
			return e
		}
		u, e = scanUpload(s.db.Row(c, "SELECT "+uploadCols+" FROM storage_upload_tasks WHERE id=$1 FOR UPDATE", id))
		if e != nil {
			return e
		}
		if u.State == "complete" {
			return syscall.ESTALE
		}
		if u.State != "uploading" {
			return nil
		}
		if state == "expired" && !time.Now().After(u.Expires) {
			return nil
		}
		if _, e = s.db.Exec(c, "DELETE FROM storage_upload_parts WHERE upload_id=$1", id); e != nil {
			return e
		}
		if _, e = s.db.Exec(c, "UPDATE user_storage SET used_bytes=used_bytes-$2 WHERE user_id=$1", u.Space, u.Size); e != nil {
			return e
		}
		if _, e = s.db.Exec(c, "UPDATE storage_upload_tasks SET state=$2 WHERE id=$1", id, state); e != nil {
			return e
		}
		return s.event(WithActor(c, u.Actor, "web", ""), u.Space, 0, "upload_"+state, u.Size, u.Name)
	})
}
func (s *Service) CancelUpload(ctx context.Context, id string) error {
	a, ok := ctx.Value(actorKey{}).(Actor)
	if !ok {
		return syscall.EACCES
	}
	return s.cancelUpload(ctx, id, a.User, "cancelled")
}
func (s *Service) ExpireUploads(ctx context.Context) error {
	rows, e := s.db.Rows(ctx, "SELECT id::text FROM storage_upload_tasks WHERE state='uploading' AND expires_at<now() ORDER BY expires_at LIMIT 20")
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		if e = s.cancelUpload(ctx, id, "", "expired"); e != nil {
			return e
		}
	}
	return nil
}
