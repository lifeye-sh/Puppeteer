package storage

import (
	"context"
	"syscall"
	"time"
	"unicode/utf8"
)

// Text replacement uses the same quota lock as RDP writes. A stale editor must
// never overwrite a file modified through the remote drive in the meantime.
func (s *Service) ReplaceText(ctx context.Context, space string, id int64, modified time.Time, text string) error {
	if e := s.authorizeAction(ctx, space, "edit"); e != nil {
		return e
	}
	if len(text) > 1<<20 || !utf8.ValidString(text) {
		return syscall.EINVAL
	}
	return s.db.Within(ctx, func(c context.Context) error {
		u, e := s.lock(c, space)
		if e != nil {
			return e
		}
		n, e := s.Get(c, space, id)
		if e != nil {
			return e
		}
		if !n.Modified.Equal(modified) {
			return syscall.ESTALE
		}
		if e = s.resize(c, n, u, int64(len(text))); e != nil {
			return e
		}
		if _, e = s.db.Exec(c, "DELETE FROM storage_chunks WHERE node_id=$1", id); e != nil {
			return e
		}
		b := []byte(text)
		for off := 0; off < len(b); off += ChunkSize {
			if _, e = s.db.Exec(c, "INSERT INTO storage_chunks(node_id,chunk_index,data) VALUES($1,$2,$3)", id, off/ChunkSize, b[off:min(off+ChunkSize, len(b))]); e != nil {
				return e
			}
		}
		if _, e = s.db.Exec(c, "UPDATE storage_nodes SET modified_at=clock_timestamp() WHERE id=$1", id); e != nil {
			return e
		}
		return s.event(c, space, id, "edit", int64(len(b)))
	})
}
