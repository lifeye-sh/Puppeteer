package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"next-terminal/backend/internal/modules/storage"
	"strconv"
	"syscall"
	"time"
)

func (a *App) routesStorageUploads(handle func(string, func(http.ResponseWriter, *http.Request) error)) {
	s := storage.New(a.db)
	handle("GET /api/storage/uploads", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		ctx, space, e := a.storageScope(r, u.ID)
		if e != nil {
			return e
		}
		out, e := s.Uploads(ctx, space)
		if e != nil {
			return storageUploadError(e)
		}
		writeJSON(w, 200, out)
		return nil
	})
	handle("POST /api/storage/uploads", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		ctx, space, e := a.storageScope(r, u.ID)
		if e != nil {
			return e
		}
		var in struct {
			Parent int64  `json:"parentId"`
			Name   string `json:"name"`
			Size   int64  `json:"size"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		out, e := s.StartUpload(ctx, space, in.Parent, in.Name, in.Size)
		if e != nil {
			return storageUploadError(e)
		}
		writeJSON(w, 201, out)
		return nil
	})
	handle("DELETE /api/storage/uploads/{id}", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		if e = s.CancelUpload(storage.WithActor(r.Context(), u.ID, "web", ""), r.PathValue("id")); e != nil {
			return storageUploadError(e)
		}
		w.WriteHeader(204)
		return nil
	})
	handle("GET /api/storage/uploads/{id}", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		ctx, space, e := a.storageScope(r, u.ID)
		if e != nil {
			return e
		}
		out, e := s.Upload(ctx, r.PathValue("id"))
		if e != nil {
			return storageUploadError(e)
		}
		if out.Space != space {
			return storageError(syscall.EACCES)
		}
		writeJSON(w, 200, out)
		return nil
	})
	handle("PUT /api/storage/uploads/{id}/parts", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		ctx, space, e := a.storageScope(r, u.ID)
		if e != nil {
			return e
		}
		task, e := s.Upload(ctx, r.PathValue("id"))
		if e != nil {
			return storageUploadError(e)
		}
		if task.Space != space {
			return storageError(syscall.EACCES)
		}
		offset, e := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		if e != nil {
			return storageError(syscall.EINVAL)
		}
		select {
		case a.fileSlots <- struct{}{}:
			defer func() { <-a.fileSlots }()
		default:
			return problem(429, "upload_busy", "上传并发已满，请重试")
		}
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if e != nil {
			return problem(413, "chunk_limit", "单个分块最大 1 MiB")
		}
		if e = s.UploadPart(ctx, task.ID, offset, b); e != nil {
			return storageUploadError(e)
		}
		w.WriteHeader(204)
		return nil
	})
	handle("POST /api/storage/uploads/{id}/complete", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		ctx, space, e := a.storageScope(r, u.ID)
		if e != nil {
			return e
		}
		task, e := s.Upload(ctx, r.PathValue("id"))
		if e != nil {
			return storageUploadError(e)
		}
		if task.Space != space {
			return storageError(syscall.EACCES)
		}
		out, e := s.FinishUpload(ctx, task.ID)
		if e != nil {
			return storageUploadError(e)
		}
		writeJSON(w, 200, out)
		return nil
	})
}

func storageUploadError(e error) error {
	if errors.Is(e, syscall.ESTALE) {
		return problem(409, "upload_conflict", "上传任务状态或分块内容已改变，请刷新核对；已完成任务不能取消")
	}
	return storageError(e)
}

func (a *App) RunStorageMaintenance(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		e := storage.New(a.db).ExpireUploads(c)
		cancel()
		if e != nil && ctx.Err() == nil {
			slog.Error("storage upload expiry failed", "error", e)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
