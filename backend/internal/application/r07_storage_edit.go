package application

import (
	"net/http"
	"next-terminal/backend/internal/modules/storage"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

func (a *App) routesStorageEdit(handle func(string, func(http.ResponseWriter, *http.Request) error)) {
	s := storage.New(a.db)
	handle("PUT /api/storage/files/{id}/name", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		ctx, space, e := a.storageScope(r, u.ID)
		if e != nil {
			return e
		}
		id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil {
			return storageError(syscall.EINVAL)
		}
		var in struct {
			Name string `json:"name"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		n, e := s.Get(ctx, space, id)
		if e != nil {
			return storageError(e)
		}
		if e = s.Rename(ctx, space, id, n.Parent, in.Name); e != nil {
			return storageError(e)
		}
		w.WriteHeader(204)
		return nil
	})
	handle("GET /api/storage/files/{id}/text", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		ctx, space, e := a.storageScope(r, u.ID)
		if e != nil {
			return e
		}
		id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil {
			return storageError(syscall.EINVAL)
		}
		n, e := s.Get(ctx, space, id)
		if e != nil {
			return storageError(e)
		}
		if n.Size > 1<<20 || n.Directory {
			return problem(422, "text_limit", "仅支持 1 MiB 以内的 UTF-8 文本文件")
		}
		b, e := s.Read(ctx, space, id, 0, int(n.Size))
		if e != nil {
			return storageError(e)
		}
		after, e := s.Get(ctx, space, id)
		if e != nil {
			return storageError(e)
		}
		if !after.Modified.Equal(n.Modified) {
			return storageError(syscall.ESTALE)
		}
		if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
			return problem(422, "text_encoding", "文件不是可编辑的 UTF-8 文本")
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"text": string(b), "modifiedAt": n.Modified})
		return nil
	})
	handle("PUT /api/storage/files/{id}/text", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		ctx, space, e := a.storageScope(r, u.ID)
		if e != nil {
			return e
		}
		id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil {
			return storageError(syscall.EINVAL)
		}
		var in struct {
			Text     string    `json:"text"`
			Modified time.Time `json:"modifiedAt"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if e = s.ReplaceText(ctx, space, id, in.Modified, in.Text); e != nil {
			return storageError(e)
		}
		w.WriteHeader(204)
		return nil
	})
}
