// Package storage implements a quota-enforced, chunked filesystem. Both HTTP
// and the RDP FUSE adapter use these operations; no writer bypasses quota locks.
package storage

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/postgres"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const ChunkSize = 65536

type Service struct{ db *postgres.DB }

func New(db *postgres.DB) *Service { return &Service{db} }

type Usage struct {
	Quota int64 `json:"quotaBytes"`
	Used  int64 `json:"usedBytes"`
}
type Node struct {
	Unavailable bool      `json:"unavailable"`
	ID          int64     `json:"id"`
	Owner       string    `json:"-"`
	Parent      int64     `json:"parentId"`
	Name        string    `json:"name"`
	Directory   bool      `json:"directory"`
	Size        int64     `json:"size"`
	Modified    time.Time `json:"modifiedAt"`
}

const cols = "id,user_id::text,coalesce(parent_id,0),name,directory,size,modified_at,payload_unavailable"

func scan(row pgx.Row) (n Node, e error) {
	e = row.Scan(&n.ID, &n.Owner, &n.Parent, &n.Name, &n.Directory, &n.Size, &n.Modified, &n.Unavailable)
	if errors.Is(e, pgx.ErrNoRows) {
		e = syscall.ENOENT
	}
	return
}
func validName(n string) bool {
	return utf8.ValidString(n) && len(n) > 0 && len(n) <= 255 && n != "." && n != ".." && !strings.ContainsAny(n, "/\\\x00:\r\n")
}
func (s *Service) Ensure(ctx context.Context, user string) (Node, error) {
	_, e := s.db.Exec(ctx, "INSERT INTO storage_spaces(id,owner_id,name) SELECT id,id,'个人存储' FROM users WHERE id=$1 ON CONFLICT DO NOTHING", user)
	if e != nil {
		return Node{}, e
	}
	var exists bool
	if e = s.db.Row(ctx, "SELECT EXISTS(SELECT 1 FROM user_storage WHERE user_id=$1)", user).Scan(&exists); e != nil {
		return Node{}, e
	}
	if !exists {
		quota, err := system.New(s.db).NewStorageQuota(ctx)
		if err != nil {
			return Node{}, err
		}
		_, e = s.db.Exec(ctx, "INSERT INTO user_storage(user_id,quota_bytes) VALUES($1,$2) ON CONFLICT DO NOTHING", user, quota)
	}
	if e != nil {
		return Node{}, e
	}
	_, e = s.db.Exec(ctx, "INSERT INTO storage_nodes(user_id,name,directory) VALUES($1,'',true) ON CONFLICT DO NOTHING", user)
	if e != nil {
		return Node{}, e
	}
	return scan(s.db.Row(ctx, "SELECT "+cols+" FROM storage_nodes WHERE user_id=$1 AND parent_id IS NULL", user))
}
func (s *Service) Usage(ctx context.Context, user string) (u Usage, e error) {
	e = s.db.Row(ctx, "SELECT quota_bytes,used_bytes FROM user_storage WHERE user_id=$1", user).Scan(&u.Quota, &u.Used)
	return
}
func (s *Service) lock(ctx context.Context, user string) (u Usage, e error) {
	if e = s.authorize(ctx, user, "write"); e != nil {
		return
	}
	e = s.db.Row(ctx, "SELECT quota_bytes,used_bytes FROM user_storage WHERE user_id=$1 FOR UPDATE", user).Scan(&u.Quota, &u.Used)
	return
}
func (s *Service) SetQuota(ctx context.Context, user string, q int64) error {
	if q < 0 || q > 1<<40 {
		return syscall.EINVAL
	}
	_, e := s.Ensure(ctx, user)
	if e != nil {
		return e
	}
	return s.db.Within(ctx, func(c context.Context) error {
		if _, e := s.lock(c, user); e != nil {
			return e
		}
		_, e := s.db.Exec(c, "UPDATE user_storage SET quota_bytes=$2,updated_at=now() WHERE user_id=$1", user, q)
		return e
	})
}
func (s *Service) Get(ctx context.Context, user string, id int64) (Node, error) {
	if e := s.authorize(ctx, user, "list"); e != nil {
		return Node{}, e
	}
	return scan(s.db.Row(ctx, "SELECT "+cols+" FROM storage_nodes WHERE user_id=$1 AND id=$2", user, id))
}
func (s *Service) Lookup(ctx context.Context, user string, parent int64, name string) (Node, error) {
	if e := s.authorize(ctx, user, "list"); e != nil {
		return Node{}, e
	}
	if !validName(name) {
		return Node{}, syscall.EINVAL
	}
	return scan(s.db.Row(ctx, "SELECT "+cols+" FROM storage_nodes WHERE user_id=$1 AND parent_id=$2 AND name=$3", user, parent, name))
}
func (s *Service) List(ctx context.Context, user string, parent int64) ([]Node, error) {
	p, e := s.Get(ctx, user, parent)
	if e != nil {
		return nil, e
	}
	if !p.Directory {
		return nil, syscall.ENOTDIR
	}
	rows, e := s.db.Rows(ctx, "SELECT "+cols+" FROM storage_nodes WHERE user_id=$1 AND parent_id=$2 ORDER BY directory DESC,name LIMIT 10001", user, parent)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		n, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	if len(out) > 10000 {
		return nil, syscall.EOVERFLOW
	}
	return out, rows.Err()
}
func (s *Service) Create(ctx context.Context, user string, parent int64, name string, dir bool) (out Node, err error) {
	action := "createFile"
	if dir {
		action = "createDir"
	}
	if err = s.authorizeAction(ctx, user, action); err != nil {
		return
	}
	if !validName(name) {
		return out, syscall.EINVAL
	}
	err = s.db.Within(ctx, func(c context.Context) error {
		if _, e := s.lock(c, user); e != nil {
			return e
		}
		p, e := s.Get(c, user, parent)
		if e != nil {
			return e
		}
		if !p.Directory {
			return syscall.ENOTDIR
		}
		var count int
		if e = s.db.Row(c, "SELECT (SELECT count(*) FROM storage_nodes WHERE user_id=$1)+(SELECT count(*) FROM storage_upload_tasks WHERE space_id=$1 AND state='uploading')", user).Scan(&count); e != nil {
			return e
		}
		if count >= 10000 {
			return syscall.ENOSPC
		}
		if _, e = s.Lookup(c, user, parent, name); e == nil {
			return syscall.EEXIST
		} else if !errors.Is(e, syscall.ENOENT) {
			return e
		}
		out, e = scan(s.db.Row(c, "INSERT INTO storage_nodes(user_id,parent_id,name,directory) VALUES($1,$2,$3,$4) RETURNING "+cols, user, parent, name, dir))
		if e != nil {
			return e
		}
		op := "create"
		if dir {
			op = "mkdir"
		}
		return s.event(c, user, out.ID, op, 0)
	})
	return
}
func (s *Service) Resize(ctx context.Context, user string, id, size int64) error {
	if e := s.authorizeAction(ctx, user, "edit"); e != nil {
		return e
	}
	return s.db.Within(ctx, func(c context.Context) error {
		u, e := s.lock(c, user)
		if e != nil {
			return e
		}
		n, e := s.Get(c, user, id)
		if e != nil {
			return e
		}
		if e := s.resize(c, n, u, size); e != nil {
			return e
		}
		return s.event(c, user, id, "truncate", size)
	})
}
func (s *Service) resize(c context.Context, n Node, u Usage, size int64) error {
	if n.Unavailable {
		return syscall.ENODATA
	}
	if n.Directory {
		return syscall.EISDIR
	}
	if size < 0 || size > 1<<40 {
		return syscall.EFBIG
	}
	delta := size - n.Size
	if delta > 0 && delta > u.Quota-u.Used {
		return syscall.EDQUOT
	}
	if size < n.Size {
		if _, e := s.db.Exec(c, "DELETE FROM storage_chunks WHERE node_id=$1 AND chunk_index>=$2", n.ID, (size+ChunkSize-1)/ChunkSize); e != nil {
			return e
		}
		if size%ChunkSize != 0 {
			if _, e := s.db.Exec(c, "UPDATE storage_chunks SET data=substring(data FROM 1 FOR $3) WHERE node_id=$1 AND chunk_index=$2", n.ID, size/ChunkSize, size%ChunkSize); e != nil {
				return e
			}
		}
	}
	if _, e := s.db.Exec(c, "UPDATE storage_nodes SET size=$2,modified_at=now() WHERE id=$1", n.ID, size); e != nil {
		return e
	}
	_, e := s.db.Exec(c, "UPDATE user_storage SET used_bytes=used_bytes+$2,updated_at=now() WHERE user_id=$1", n.Owner, delta)
	return e
}
func (s *Service) Write(ctx context.Context, user string, id, off int64, data []byte) error {
	if e := s.authorizeAction(ctx, user, "edit"); e != nil {
		return e
	}
	count := int64(len(data))
	if off < 0 || off > 1<<40 || len(data) > 4*1024*1024 {
		return syscall.EINVAL
	}
	return s.db.Within(ctx, func(c context.Context) error {
		u, e := s.lock(c, user)
		if e != nil {
			return e
		}
		n, e := s.Get(c, user, id)
		if e != nil {
			return e
		}
		end := off + int64(len(data))
		if n.Unavailable {
			return syscall.ENODATA
		}
		if n.Directory {
			return syscall.EISDIR
		}
		if end > n.Size {
			if e = s.resize(c, n, u, end); e != nil {
				return e
			}
		}
		for len(data) > 0 {
			index := off / ChunkSize
			start := int(off % ChunkSize)
			take := min(len(data), ChunkSize-start)
			var old []byte
			e = s.db.Row(c, "SELECT data FROM storage_chunks WHERE node_id=$1 AND chunk_index=$2", id, index).Scan(&old)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			buf := make([]byte, max(len(old), start+take))
			copy(buf, old)
			copy(buf[start:], data[:take])
			if _, e = s.db.Exec(c, "INSERT INTO storage_chunks(node_id,chunk_index,data) VALUES($1,$2,$3) ON CONFLICT(node_id,chunk_index) DO UPDATE SET data=excluded.data", id, index, buf); e != nil {
				return e
			}
			off += int64(take)
			data = data[take:]
		}
		_, e = s.db.Exec(c, "UPDATE storage_nodes SET modified_at=now() WHERE id=$1", id)
		if e != nil {
			return e
		}
		return s.event(c, user, id, "write", count)
	})
}
func (s *Service) Read(ctx context.Context, user string, id, off int64, size int) ([]byte, error) {
	if e := s.authorize(ctx, user, "read"); e != nil {
		return nil, e
	}
	n, e := s.Get(ctx, user, id)
	if e != nil {
		return nil, e
	}
	if n.Unavailable {
		return nil, syscall.ENODATA
	}
	if n.Directory {
		return nil, syscall.EISDIR
	}
	if off < 0 || size < 0 || size > 4*1024*1024 {
		return nil, syscall.EINVAL
	}
	if off >= n.Size {
		return []byte{}, nil
	}
	size = int(min(int64(size), n.Size-off))
	out := make([]byte, size)
	pos := 0
	for pos < size {
		index := off / ChunkSize
		start := int(off % ChunkSize)
		take := min(size-pos, ChunkSize-start)
		var data []byte
		e = s.db.Row(ctx, "SELECT data FROM storage_chunks WHERE node_id=$1 AND chunk_index=$2", id, index).Scan(&data)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return nil, e
		}
		if start < len(data) {
			copy(out[pos:pos+take], data[start:min(len(data), start+take)])
		}
		pos += take
		off += int64(take)
	}
	if e := s.event(ctx, user, id, "read", int64(len(out))); e != nil {
		return nil, e
	}
	return out, nil
}
func (s *Service) Remove(ctx context.Context, user string, id int64) error {
	if e := s.authorizeAction(ctx, user, "delete"); e != nil {
		return e
	}
	return s.db.Within(ctx, func(c context.Context) error {
		if _, e := s.lock(c, user); e != nil {
			return e
		}
		n, e := s.Get(c, user, id)
		if e != nil {
			return e
		}
		var pending bool
		if e = s.db.Row(c, "SELECT EXISTS(SELECT 1 FROM storage_upload_tasks WHERE parent_id=$1 AND state='uploading')", id).Scan(&pending); e != nil {
			return e
		}
		if pending {
			return syscall.ENOTEMPTY
		}
		if n.Parent == 0 {
			return syscall.EPERM
		}
		var exists bool
		if e = s.db.Row(c, "SELECT EXISTS(SELECT 1 FROM storage_nodes WHERE parent_id=$1)", id).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return syscall.ENOTEMPTY
		}
		if _, e = s.db.Exec(c, "DELETE FROM storage_nodes WHERE id=$1", id); e != nil {
			return e
		}
		_, e = s.db.Exec(c, "UPDATE user_storage SET used_bytes=used_bytes-$2 WHERE user_id=$1", user, n.Size)
		if e != nil {
			return e
		}
		return s.event(c, user, id, "delete", n.Size, n.Name)
	})
}
func (s *Service) Rename(ctx context.Context, user string, id, parent int64, name string) error {
	return s.rename(ctx, user, id, parent, name, false)
}

// RenameReplace supports atomic desktop editor saves using temporary files.
func (s *Service) RenameReplace(ctx context.Context, user string, id, parent int64, name string) error {
	return s.rename(ctx, user, id, parent, name, true)
}
func (s *Service) rename(ctx context.Context, user string, id, parent int64, name string, replace bool) error {
	if e := s.authorizeAction(ctx, user, "rename"); e != nil {
		return e
	}
	if replace {
		if e := s.authorizeAction(ctx, user, "edit"); e != nil {
			return e
		}
	}
	if !validName(name) {
		return syscall.EINVAL
	}
	return s.db.Within(ctx, func(c context.Context) error {
		if _, e := s.lock(c, user); e != nil {
			return e
		}
		n, e := s.Get(c, user, id)
		if e != nil {
			return e
		}
		if n.Parent == 0 {
			return syscall.EPERM
		}
		p, e := s.Get(c, user, parent)
		if e != nil {
			return e
		}
		if !p.Directory {
			return syscall.ENOTDIR
		}
		for ancestor := p; ancestor.Parent != 0; {
			if ancestor.ID == id {
				return syscall.EINVAL
			}
			ancestor, e = s.Get(c, user, ancestor.Parent)
			if e != nil {
				return e
			}
		}
		if other, e := s.Lookup(c, user, parent, name); e == nil {
			if other.ID == id {
				return nil
			}
			if !replace {
				return syscall.EEXIST
			}
			if other.Directory != n.Directory {
				return syscall.EISDIR
			}
			var children bool
			if e = s.db.Row(c, "SELECT EXISTS(SELECT 1 FROM storage_nodes WHERE parent_id=$1)", other.ID).Scan(&children); e != nil {
				return e
			}
			if children {
				return syscall.ENOTEMPTY
			}
			if _, e = s.db.Exec(c, "DELETE FROM storage_nodes WHERE id=$1 AND user_id=$2", other.ID, user); e != nil {
				return e
			}
			if _, e = s.db.Exec(c, "UPDATE user_storage SET used_bytes=used_bytes-$2 WHERE user_id=$1", user, other.Size); e != nil {
				return e
			}
		} else if !errors.Is(e, syscall.ENOENT) {
			return e
		}
		_, e = s.db.Exec(c, "UPDATE storage_nodes SET parent_id=$2,name=$3,modified_at=now() WHERE id=$1", id, parent, name)
		if e != nil {
			return e
		}
		return s.event(c, user, id, "rename", 0)
	})
}
