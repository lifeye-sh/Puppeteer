package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"next-terminal/backend/internal/modules/storage"
	"strconv"
	"syscall"
)

func storageError(e error) error {
	switch {
	case e == nil:
		return nil
	case errors.Is(e, syscall.ENODATA):
		return problem(409, "file_payload_unavailable", "该备份未包含文件内容，请恢复完整备份后访问")
	case errors.Is(e, syscall.EACCES), errors.Is(e, syscall.EROFS):
		return problem(403, "storage_denied", "存储授权或通道策略禁止此操作")
	case errors.Is(e, syscall.ENOENT):
		return problem(404, "file_not_found", "文件不存在或无权访问")
	case errors.Is(e, syscall.EDQUOT):
		return problem(422, "storage_quota", "个人存储空间不足，请删除文件或联系管理员调整配额")
	case errors.Is(e, syscall.ESTALE):
		return problem(409, "file_changed", "文件已被其他操作修改，请重新打开后编辑")
	case errors.Is(e, syscall.EEXIST):
		return problem(409, "file_exists", "同名文件已存在")
	case errors.Is(e, syscall.ENOTEMPTY):
		return problem(409, "directory_not_empty", "请先清空目录")
	case errors.Is(e, syscall.EINVAL), errors.Is(e, syscall.EPERM), errors.Is(e, syscall.EISDIR), errors.Is(e, syscall.ENOTDIR):
		return problem(422, "invalid_file_operation", "文件名、路径或操作无效")
	case errors.Is(e, syscall.ENOSPC):
		return problem(422, "file_count_limit", "文件数量达到上限")
	}
	return e
}
func (a *App) routesStorage(handle func(string, func(http.ResponseWriter, *http.Request) error)) {
	s := storage.New(a.db)
	a.routesStorageAdmin(handle)
	a.routesStorageEdit(handle)
	a.routesStorageUploads(handle)
	handle("GET /api/storage", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		ctx, space, e := a.storageScope(r, u.ID)
		if e != nil {
			return e
		}
		root, e := s.Ensure(ctx, space)
		if e != nil {
			return e
		}
		parent := root.ID
		if raw := r.URL.Query().Get("parent"); raw != "" {
			parent, e = strconv.ParseInt(raw, 10, 64)
			if e != nil {
				return storageError(syscall.EINVAL)
			}
		}
		node, e := s.Get(ctx, space, parent)
		if e != nil {
			return storageError(e)
		}
		items, e := s.List(ctx, space, parent)
		if e != nil {
			return storageError(e)
		}
		usage, e := s.Usage(ctx, space)
		if e != nil {
			return e
		}
		p, e := s.Policy(ctx, u.ID)
		if e != nil {
			return e
		}
		writable := s.CanWrite(ctx, space) == nil
		writeJSON(w, 200, map[string]any{"spaceId": space, "writable": writable, "download": p.Download, "rootId": root.ID, "folder": node, "items": items, "usage": usage, "rdpMounted": a.config.RDPDriveEnabled})
		return nil
	})
	handle("POST /api/storage/folders", func(w http.ResponseWriter, r *http.Request) error {
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
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		n, e := s.Create(ctx, space, in.Parent, in.Name, true)
		if e != nil {
			return storageError(e)
		}
		writeJSON(w, 200, n)
		return a.audit.Detail(r.Context(), u.ID, "storage.mkdir", requestID(r), sourceIP(r), map[string]string{"nodeId": fmt.Sprint(n.ID)})
	})
	handle("POST /api/storage/upload", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		ctx, space, e := a.storageScope(r, u.ID)
		if e != nil {
			return e
		}
		parent, e := strconv.ParseInt(r.URL.Query().Get("parent"), 10, 64)
		if e != nil {
			return storageError(syscall.EINVAL)
		}
		const limit = int64(1 << 30)
		if r.ContentLength < 0 || r.ContentLength > limit {
			return problem(413, "upload_limit", "单文件最大 1 GiB，上传必须提供长度")
		}
		select {
		case a.fileSlots <- struct{}{}:
			defer func() { <-a.fileSlots }()
		default:
			return problem(429, "upload_busy", "上传并发已满，请稍后重试")
		}
		task, e := s.StartUpload(ctx, space, parent, r.URL.Query().Get("name"), r.ContentLength)
		if e != nil {
			return storageError(e)
		}
		complete := false
		defer func() {
			if !complete {
				c, cancel := context.WithTimeout(context.Background(), 10_000_000_000)
				defer cancel()
				_ = s.CancelUpload(storage.WithActor(c, u.ID, "web", ""), task.ID)
			}
		}()
		for off := int64(0); off < r.ContentLength; {
			b := make([]byte, min(int64(1<<20), r.ContentLength-off))
			if _, e = io.ReadFull(r.Body, b); e != nil {
				return problem(400, "upload_incomplete", "上传中断，未发布文件")
			}
			if _, e = a.authenticated(r); e != nil {
				return e
			}
			if e = s.UploadPart(ctx, task.ID, off, b); e != nil {
				return storageError(e)
			}
			off += int64(len(b))
		}
		task, e = s.FinishUpload(ctx, task.ID)
		if e != nil {
			return storageError(e)
		}
		complete = true
		writeJSON(w, 200, map[string]any{"id": task.Node})
		return nil
	})
	handle("GET /api/storage/files/{id}", func(w http.ResponseWriter, r *http.Request) error {
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
		if n.Directory {
			return storageError(syscall.EISDIR)
		}
		policy, e := s.Policy(ctx, u.ID)
		if e != nil {
			return e
		}
		if !policy.Download {
			return storageError(syscall.EACCES)
		}
		if e = a.audit.Detail(r.Context(), u.ID, "storage.download", requestID(r), sourceIP(r), map[string]string{"nodeId": fmt.Sprint(id)}); e != nil {
			return e
		}
		w.Header().Set("Content-Length", strconv.FormatInt(n.Size, 10))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": n.Name}))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		for off := int64(0); off < n.Size; {
			if _, e = a.authenticated(r); e != nil {
				return nil
			}
			b, e := s.Read(ctx, space, id, off, int(min(int64(1<<20), n.Size-off)))
			if e != nil || len(b) == 0 {
				return nil
			}
			if _, e = w.Write(b); e != nil {
				return nil
			}
			off += int64(len(b))
		}
		return nil
	})
	handle("DELETE /api/storage/files/{id}", func(w http.ResponseWriter, r *http.Request) error {
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
		if e = s.Remove(ctx, space, id); e != nil {
			return storageError(e)
		}
		if e = a.audit.Detail(r.Context(), u.ID, "storage.delete", requestID(r), sourceIP(r), map[string]string{"nodeId": fmt.Sprint(id)}); e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	handle("GET /api/admin/users/{id}/storage", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		id := r.PathValue("id")
		if _, e := s.Ensure(r.Context(), id); e != nil {
			return e
		}
		u, e := s.Usage(r.Context(), id)
		if e != nil {
			return e
		}
		writeJSON(w, 200, u)
		return nil
	})
	handle("PUT /api/admin/users/{id}/storage", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in struct {
			Quota int64 `json:"quotaBytes"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if e = s.SetQuota(r.Context(), r.PathValue("id"), in.Quota); e != nil {
			return storageError(e)
		}
		if e = a.audit.Detail(r.Context(), u.ID, "storage.quota", requestID(r), sourceIP(r), map[string]string{"userId": r.PathValue("id"), "quotaBytes": fmt.Sprint(in.Quota)}); e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
}
