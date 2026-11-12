package application

import (
	"context"
	"errors"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"io"
	"mime"
	"net/http"
	"next-terminal/backend/internal/modules/files"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type fileAccess struct {
	Write   bool
	Session session.Session
	User    *identity.User
	Live    *liveSession
	Client  *ssh.Client
}

func (a *App) fileAccess(r *http.Request, write bool) (v fileAccess, e error) {
	v.Write = write
	v.Session, v.User, e = a.inspectSession(r, "identity.manage")
	if e != nil {
		return
	}
	if v.Session.ActorID != v.User.ID || v.Session.State != "connected" || (write && v.Session.Readonly) {
		return v, fault.Denied
	}
	if _, e = a.checkLive(r.Context(), v.Session, token(r)); e != nil {
		return
	}
	if write {
		if e = a.db.RequireFeature(r.Context(), "files.write"); e != nil {
			return
		}
	}
	if strings.Contains(r.URL.Path, "/uploads") {
		if e = a.enforcePolicy(r.Context(), v.User, v.Session.AssetID, v.Session.ClientIP, "upload", ""); e != nil {
			return v, e
		}
	}
	a.liveMu.Lock()
	v.Live = a.live[v.Session.ID]
	if v.Live != nil {
		v.Client = v.Live.client
	}
	a.liveMu.Unlock()
	if v.Live == nil || v.Client == nil {
		return v, fault.Conflict
	}
	return
}

// Close the SSH subsystem on HTTP cancellation, session revocation or timeout.
// A stuck SSH channel-open handshake closes the transport rather than leaking work.
func openSFTP(ctx context.Context, client *ssh.Client) (*sftp.Client, func(), error) {
	type result struct {
		s *ssh.Session
		e error
	}
	ready := make(chan result, 1)
	go func() { s, e := client.NewSession(); ready <- result{s, e} }()
	var s *ssh.Session
	select {
	case v := <-ready:
		if v.e != nil {
			return nil, nil, v.e
		}
		s = v.s
	case <-ctx.Done():
		_ = client.Close()
		v := <-ready
		if v.s != nil {
			_ = v.s.Close()
		}
		return nil, nil, ctx.Err()
	}
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	closeAll := func() { stop(); _ = s.Close() }
	in, e := s.StdinPipe()
	if e != nil {
		closeAll()
		return nil, nil, e
	}
	out, e := s.StdoutPipe()
	if e != nil {
		closeAll()
		return nil, nil, e
	}
	if e = s.RequestSubsystem("sftp"); e != nil {
		closeAll()
		return nil, nil, e
	}
	c, e := sftp.NewClientPipe(out, in, sftp.UseConcurrentReads(false), sftp.UseConcurrentWrites(false))
	if e != nil {
		closeAll()
		return nil, nil, e
	}
	return c, func() { closeAll(); _ = c.Close() }, nil
}
func (a *App) withFiles(r *http.Request, v fileAccess, fn func(context.Context, files.Remote) error) error {
	if v.Write {
		v.Write = false // Guard once around the entire SFTP operation, including connect.
		return a.db.RunAdmitted(r.Context(), "files.write", func(ctx context.Context) error { return a.withFiles(r.WithContext(ctx), v, fn) })
	}
	select {
	case a.fileSlots <- struct{}{}:
		defer func() { <-a.fileSlots }()
	default:
		return problem(429, "file_busy", "文件操作并发已满，请稍后重试")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	stop := context.AfterFunc(v.Live.ctx, cancel)
	defer stop()
	c, closeSFTP, e := openSFTP(ctx, v.Client)
	if e != nil {
		return fileError(e)
	}
	defer closeSFTP()
	// Browse the root exposed by the remote SFTP server. OS permissions (and
	// any server-side chroot) still apply; this does not elevate the SSH user.
	root, e := c.RealPath("/")
	if e != nil {
		return fileError(e)
	}
	saved, e := a.files.Mount(ctx, v.Session.ID, root)
	if e != nil {
		return e
	}
	if saved != root {
		return fault.Conflict
	}
	remote := files.Remote{Client: c, Root: root}
	return fileError(fn(ctx, remote))
}
func fileError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, fault.Invalid) || errors.Is(e, fault.Conflict) || errors.Is(e, fault.Denied) || errors.Is(e, postgres.ErrAdmission) {
		return e
	}
	var api *apiError
	if errors.As(e, &api) {
		return e
	}
	if os.IsNotExist(e) {
		return problem(404, "file_missing", "文件或目录不存在")
	}
	return problem(422, "sftp_failed", "SFTP 操作失败，请检查权限、连接或目标状态；不要自动重试不确定的写操作")
}
func (a *App) fileList(w http.ResponseWriter, r *http.Request) error {
	v, e := a.fileAccess(r, false)
	if e != nil {
		return e
	}
	rel, e := files.Normalize(r.URL.Query().Get("path"))
	if e != nil {
		return e
	}
	return a.withFiles(r, v, func(ctx context.Context, remote files.Remote) error {
		// Only known temporary files belonging to this actor/asset/root are eligible.
		if !v.Session.Readonly {
			tasks, e := a.files.CleanupTasks(ctx, v.Session.AssetID, v.User.ID, remote.Root)
			if e != nil {
				return e
			}
			for _, t := range tasks {
				full, e := remote.Resolve(t.Path, true)
				if e != nil {
					continue
				}
				tmp := files.TempName(full, t.ID)
				if e = remote.Client.Remove(tmp); e == nil || os.IsNotExist(e) {
					_ = a.files.SetCleanup(ctx, t.ID, false)
				}
			}
		}
		entries, e := remote.List(ctx, rel)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"root": remote.Root, "path": rel, "items": entries, "maxFileBytes": files.MaxFile, "maxChunkBytes": files.MaxChunk, "readonly": v.Session.Readonly})
		return nil
	})
}
func (a *App) logFile(ctx context.Context, v fileAccess, action, src, dest string, fn func() (int64, error)) error {
	id, e := a.audit.StartFile(ctx, v.Session.ID, v.User.ID, action, src, dest)
	if e != nil {
		return e
	}
	n, opErr := fn()
	state, message := "succeeded", ""
	if opErr != nil {
		state = "failed"
		message = "操作未成功或结果待核对"
	}
	end, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if e = a.audit.EndFile(end, id, state, message, n); e != nil {
		return problem(503, "file_audit_incomplete", "操作结果审计保存失败，请核对目标，不要自动重试")
	}
	return opErr
}
func (a *App) fileRead(w http.ResponseWriter, r *http.Request) error {
	v, e := a.fileAccess(r, false)
	if e != nil {
		return e
	}
	if e = a.enforcePolicy(r.Context(), v.User, v.Session.AssetID, v.Session.ClientIP, "download", ""); e != nil {
		return e
	}
	rel, e := files.Normalize(r.URL.Query().Get("path"))
	if e != nil {
		return e
	}
	return a.withFiles(r, v, func(ctx context.Context, remote files.Remote) error {
		var data []byte
		limit := int64(files.MaxFile)
		action := "download"
		if r.URL.Query().Get("text") == "true" {
			limit = 256 * 1024
			action = "read_text"
		}
		e := a.logFile(ctx, v, action, rel, "", func() (int64, error) {
			var err error
			data, err = remote.Read(rel, limit)
			if err == nil && action == "read_text" && (!utf8.Valid(data) || strings.ContainsRune(string(data), 0)) {
				err = fault.Invalid
			}
			return int64(len(data)), err
		})
		if e != nil {
			return e
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if action == "read_text" {
			writeJSON(w, 200, map[string]any{"content": string(data), "sha256": files.Hash(data)})
			return nil
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(rel)}))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, e = w.Write(data)
		return e
	})
}
func (a *App) fileMutate(w http.ResponseWriter, r *http.Request) error {
	v, e := a.fileAccess(r, true)
	if e != nil {
		return e
	}
	var in struct {
		Action      string `json:"action"`
		Path        string `json:"path"`
		Destination string `json:"destination"`
		Expected    string `json:"expectedHash"`
		Mode        string `json:"mode"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	action := in.Action
	if action == "mkdir" {
		action = "createDir"
	}
	if e = a.enforcePolicy(r.Context(), v.User, v.Session.AssetID, v.Session.ClientIP, action, ""); e != nil {
		return e
	}
	rel, e := files.Normalize(in.Path)
	if e != nil || rel == "" {
		return fault.Invalid
	}
	dest, e := files.Normalize(in.Destination)
	if e != nil {
		return e
	}
	if in.Action != "mkdir" && in.Action != "rename" && in.Action != "delete" && in.Action != "chmod" {
		return fault.Invalid
	}
	return a.withFiles(r, v, func(ctx context.Context, remote files.Remote) error {
		return a.files.LockAsset(ctx, v.Session.AssetID, func(lockCtx context.Context) error {
			return a.logFile(ctx, v, in.Action, rel, dest, func() (int64, error) {
				full, e := remote.Resolve(rel, in.Action == "mkdir")
				if e != nil {
					return 0, e
				}
				switch in.Action {
				case "chmod":
					mode, e := strconv.ParseUint(in.Mode, 8, 9)
					if e != nil || len(in.Mode) != 3 {
						return 0, fault.Invalid
					}
					return 0, remote.Client.Chmod(full, os.FileMode(mode))
				case "mkdir":
					return 0, remote.Client.Mkdir(full)
				case "delete":
					info, e := remote.Client.Lstat(full)
					if e != nil {
						return 0, e
					}
					if info.IsDir() {
						return 0, remote.Client.RemoveDirectory(full)
					}
					if !info.Mode().IsRegular() {
						return 0, fault.Denied
					}
					return info.Size(), remote.Client.Remove(full)
				case "rename":
					if dest == "" {
						return 0, fault.Invalid
					}
					target, e := remote.Resolve(dest, true)
					if e != nil {
						return 0, e
					}
					if _, e = remote.Client.Lstat(target); !os.IsNotExist(e) {
						return 0, fault.Conflict
					}
					return 0, remote.Client.Rename(full, target)
				}
				return 0, fault.Invalid
			})
		})
	})
}
func (a *App) uploadCreate(w http.ResponseWriter, r *http.Request) error {
	v, e := a.fileAccess(r, true)
	if e != nil {
		return e
	}
	var in struct {
		ID       string `json:"id"`
		Path     string `json:"path"`
		Size     int64  `json:"size"`
		Expected string `json:"expectedHash"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	rel, e := files.Normalize(in.Path)
	if e != nil || rel == "" || !resource.ValidID(in.ID) || in.Size < 0 || in.Size > files.MaxFile || (in.Expected != "" && (len(in.Expected) != 64 || strings.Trim(in.Expected, "0123456789abcdef") != "")) {
		return fault.Invalid
	}
	action := "createFile"
	if in.Expected != "" {
		action = "edit"
	}
	if e = a.enforcePolicy(r.Context(), v.User, v.Session.AssetID, v.Session.ClientIP, action, ""); e != nil {
		return e
	}
	return a.withFiles(r, v, func(ctx context.Context, remote files.Remote) error {
		if _, e := remote.CheckTarget(rel, in.Expected); e != nil {
			return e
		}
		task, e := a.files.Create(ctx, files.Task{ID: in.ID, SessionID: v.Session.ID, ActorID: v.User.ID, AssetID: v.Session.AssetID, Root: remote.Root, Path: rel, Size: in.Size, Expected: in.Expected})
		if e != nil {
			return e
		}
		if e = a.audit.StartUpload(ctx, task.ID, v.Session.ID, v.User.ID, rel); e != nil {
			_ = a.files.Cancel(ctx, task.ID, v.User.ID)
			return e
		}
		writeJSON(w, 200, task)
		return nil
	})
}
func (a *App) ownedTask(r *http.Request) (files.Task, *identity.User, error) {
	u, e := a.authenticated(r)
	if e != nil {
		return files.Task{}, nil, e
	}
	id := r.PathValue("task")
	if !resource.ValidID(id) {
		return files.Task{}, u, fault.Invalid
	}
	t, e := a.files.Task(r.Context(), id, u.ID)
	return t, u, e
}
func (a *App) uploadStatus(w http.ResponseWriter, r *http.Request) error {
	t, _, e := a.ownedTask(r)
	if e != nil {
		return e
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, t)
	return nil
}
func (a *App) uploadCancel(w http.ResponseWriter, r *http.Request) error {
	t, u, e := a.ownedTask(r)
	if e != nil {
		return e
	}
	if e = a.files.Cancel(r.Context(), t.ID, u.ID); e != nil {
		return e
	}
	if e = a.audit.SyncUploads(r.Context()); e != nil {
		return e
	}
	return a.uploadStatus(w, r)
}
func (a *App) uploadChunk(w http.ResponseWriter, r *http.Request) error {
	t, u, e := a.ownedTask(r)
	if e != nil {
		return e
	}
	r.SetPathValue("id", t.SessionID)
	if _, e = a.fileAccess(r, true); e != nil {
		return e
	}
	offset, e := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if e != nil {
		return fault.Invalid
	}
	data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, files.MaxChunk))
	if e != nil {
		return problem(413, "chunk_too_large", "分片不得超过 1 MiB")
	}
	if e = a.files.Chunk(r.Context(), t.ID, u.ID, offset, data); e != nil {
		return e
	}
	return a.uploadStatus(w, r)
}
func (a *App) uploadCommit(w http.ResponseWriter, r *http.Request) error {
	t, _, e := a.ownedTask(r)
	if e != nil {
		return e
	}
	if t.State == "succeeded" {
		writeJSON(w, 200, t)
		return nil
	}
	r.SetPathValue("id", t.SessionID)
	v, e := a.fileAccess(r, true)
	if e != nil {
		return e
	}
	action := "createFile"
	if t.Expected != "" {
		action = "edit"
	}
	if e = a.enforcePolicy(r.Context(), v.User, v.Session.AssetID, v.Session.ClientIP, action, ""); e != nil {
		return e
	}
	if e = a.files.Claim(r.Context(), t.ID, v.User.ID); e != nil {
		return e
	}
	finalState, message, hash := "failed", "传输失败；目标状态请核对", ""
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if current, e := a.files.Task(ctx, t.ID, v.User.ID); e == nil && current.Cancel && finalState == "failed" {
			finalState = "canceled"
			message = "已取消"
		}
		_ = a.files.Finish(ctx, t.ID, finalState, message, hash)
		_ = a.audit.SyncUploads(ctx)
	}()
	e = a.withFiles(r, v, func(ctx context.Context, remote files.Remote) error {
		if remote.Root != t.Root {
			return fault.Conflict
		}
		return a.files.LockAsset(ctx, v.Session.AssetID, func(lockCtx context.Context) error {
			_, operationError := func() (int64, error) {
				full, e := remote.CheckTarget(t.Path, t.Expected)
				if e != nil {
					return 0, e
				}
				data, e := a.files.Data(lockCtx, t.ID)
				if e != nil {
					return 0, e
				}
				if int64(len(data)) != t.Size {
					return 0, fault.Conflict
				}
				hash = files.Hash(data)
				mode := os.FileMode(0600)
				if t.Expected != "" {
					info, err := remote.Client.Lstat(full)
					if err != nil {
						return 0, err
					}
					mode = info.Mode().Perm()
				}
				tmp := files.TempName(full, t.ID)
				if e = a.files.SetCleanup(ctx, t.ID, true); e != nil {
					return 0, e
				}
				f, e := remote.Client.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
				if e != nil {
					return 0, e
				}
				defer func() { _ = f.Close() }()
				if e = f.Chmod(0600); e != nil {
					return 0, e
				}
				for offset := 0; offset < len(data); {
					current, e := a.files.Task(ctx, t.ID, v.User.ID)
					if e != nil {
						return 0, e
					}
					if current.Cancel {
						finalState = "canceled"
						message = "已取消"
						return 0, context.Canceled
					}
					end := min(offset+65536, len(data))
					if _, e = f.Write(data[offset:end]); e != nil {
						return 0, e
					}
					offset = end
				}
				if _, ok := remote.Client.HasExtension("fsync@openssh.com"); ok {
					if e = f.Sync(); e != nil {
						return 0, e
					}
				}
				if e = f.Close(); e != nil {
					return 0, e
				}
				actual, e := remote.ReadAbsolute(tmp, files.MaxFile)
				if e != nil {
					return 0, e
				}
				if files.Hash(actual) != hash {
					return 0, fault.Conflict
				}
				if _, e = remote.CheckTarget(t.Path, t.Expected); e != nil {
					return 0, e
				}
				if _, e = a.checkLive(ctx, v.Session, token(r)); e != nil {
					return 0, e
				}
				if e = remote.Client.Chmod(tmp, mode); e != nil {
					return 0, e
				}
				if e = a.files.Committing(ctx, t.ID); e != nil {
					return 0, e
				}
				// From this point cancellation cannot remove the final file. An uncertain
				// rename result is retained as interrupted, never automatically retried.
				finalState = "interrupted"
				message = "提交结果待核对，请下载目标比较 SHA256 后决定是否重试"
				if t.Expected != "" {
					e = remote.Client.PosixRename(tmp, full)
				} else {
					e = remote.Client.Rename(tmp, full)
				}
				if e != nil {
					return 0, e
				}
				if e = a.files.SetCleanup(ctx, t.ID, false); e != nil {
					return t.Size, e
				}
				finalState = "succeeded"
				message = "目标写入并校验完成"
				return t.Size, nil
			}()
			return operationError
		})
	})
	// Close/cancel may invalidate the SFTP channel; next authorized mount retries cleanup.
	if e != nil {
		return e
	}
	if e = a.files.Finish(r.Context(), t.ID, finalState, message, hash); e != nil {
		return e
	}
	if e = a.audit.SyncUploads(r.Context()); e != nil {
		return e
	}
	return a.uploadStatus(w, r)
}
func (a *App) fileLogs(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	actor := u.ID
	if r.URL.Query().Get("all") == "true" {
		if e = a.allowed(r.Context(), u, "audit.read"); e != nil {
			return e
		}
		actor = ""
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("current"))
	if page < 1 {
		page = 1
	}
	if page > 1000000 {
		return fault.Invalid
	}
	items, total, e := a.audit.FilePage(r.Context(), actor, (page-1)*20)
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total})
	return nil
}
func (a *App) routesR05(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("GET /api/recordings/retention-preview", a.recordingRetentionPreview)
	h("GET /api/sessions/{id}/files", a.fileList)
	h("GET /api/sessions/{id}/files/content", a.fileRead)
	h("POST /api/sessions/{id}/files/actions", a.fileMutate)
	h("POST /api/sessions/{id}/uploads", a.uploadCreate)
	h("GET /api/uploads/{task}", a.uploadStatus)
	h("PUT /api/uploads/{task}/chunks", a.uploadChunk)
	h("POST /api/uploads/{task}/commit", a.uploadCommit)
	h("POST /api/uploads/{task}/cancel", a.uploadCancel)
	h("GET /api/filesystem-logs", a.fileLogs)
}
func (a *App) recordingRetentionPreview(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "audit.read"); e != nil {
		return e
	}
	days, e := strconv.Atoi(r.URL.Query().Get("days"))
	if e != nil || (days != -1 && (days < 1 || days > 3650)) {
		return fault.Invalid
	}
	data, e := a.audit.RetentionPreview(r.Context(), days)
	if e != nil {
		return e
	}
	writeJSON(w, 200, data)
	return nil
}
func (a *App) RecoverFiles(ctx context.Context) error {
	if e := a.files.Recover(ctx, true); e != nil {
		return e
	}
	return a.audit.InterruptFiles(ctx)
}
