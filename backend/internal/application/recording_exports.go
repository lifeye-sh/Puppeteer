package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"next-terminal/backend/internal/platform/jobs"
	"os"
	"path/filepath"
	"time"
)

func (a *App) mp4Available() bool {
	return a.config.MP4URL != "" && len(a.config.MP4Token) >= 32 && a.config.MP4Directory != ""
}
func (a *App) recordingExportStatus(w http.ResponseWriter, r *http.Request) error {
	v, _, e := a.inspectSession(r, "audit.read")
	if e != nil {
		return e
	}
	status, e := a.audit.Export(r.Context(), v.ID)
	if e != nil {
		return e
	}
	if status.State == "succeeded" {
		info, e := os.Stat(filepath.Join(a.config.MP4Directory, status.ID+".mp4"))
		if e != nil || !info.Mode().IsRegular() || info.Size() != status.Size {
			_, e = a.db.Exec(r.Context(), `UPDATE jobs SET state='failed',reason='cache_missing' WHERE id=$1 AND state='succeeded'`, status.ID)
			if e != nil {
				return e
			}
			status.State = "failed"
			status.Reason = "cache_missing"
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"available": a.mp4Available(), "export": status})
	return nil
}
func (a *App) requestRecordingExport(w http.ResponseWriter, r *http.Request) error {
	v, u, e := a.inspectSession(r, "audit.read")
	if e != nil {
		return e
	}
	if !a.mp4Available() {
		return problem(503, "converter_unavailable", "后台转换服务尚未配置")
	}
	page, e := a.audit.Recording(r.Context(), v.ID, 0)
	if e != nil {
		return e
	}
	if page.State == "writing" || page.Total == 0 {
		return problem(409, "recording_unavailable", "请等待录屏结束，且录屏需有内容")
	}
	if page.Bytes > 100<<20 || page.DurationMS > 7200000 {
		return problem(422, "recording_limit", "后台转换支持最长 2 小时、原始录屏最大 100 MiB")
	}
	status, e := a.audit.RequestExport(r.Context(), v.ID, u.ID)
	if e != nil {
		return e
	}
	writeJSON(w, 202, status)
	return nil
}
func (a *App) cancelRecordingExport(w http.ResponseWriter, r *http.Request) error {
	v, u, e := a.inspectSession(r, "audit.read")
	if e != nil {
		return e
	}
	e = a.db.Within(r.Context(), func(c context.Context) error {
		status, e := a.audit.Export(c, v.ID)
		if e != nil {
			return e
		}
		if status.ID == "" {
			return nil
		}
		if e = jobs.New(a.db).Cancel(c, status.ID); e != nil {
			return e
		}
		if _, e = a.db.Exec(c, `UPDATE jobs SET state='cancelled',ended_at=now(),reason='cancelled' WHERE id=$1 AND state='queued'`, status.ID); e != nil {
			return e
		}
		return a.audit.Detail(c, u.ID, "recording.mp4_cancelled", requestID(r), sourceIP(r), map[string]string{"sessionId": v.ID})
	})
	if e != nil {
		return e
	}
	w.WriteHeader(204)
	return nil
}
func (a *App) downloadRecordingMP4(w http.ResponseWriter, r *http.Request) error {
	v, u, e := a.inspectSession(r, "audit.read")
	if e != nil {
		return e
	}
	var invalid string
	var file *os.File
	var modified time.Time
	defer func() {
		if file != nil {
			file.Close()
		}
	}()
	e = a.db.Within(r.Context(), func(c context.Context) error {
		// Pin the immutable export while it is verified. An open descriptor
		// still serves this download if retention subsequently unlinks it.
		if e := a.audit.LockRecordingExport(c, v.ID); e != nil {
			return e
		}
		var id string
		if e := a.db.Row(c, `SELECT job_id::text FROM recording_exports WHERE session_id=$1 FOR SHARE`, v.ID).Scan(&id); e != nil {
			return e
		}
		status, e := a.audit.Export(c, v.ID)
		if e != nil {
			return e
		}
		if status.State != "succeeded" {
			return problem(409, "mp4_not_ready", "MP4 尚未生成完成")
		}
		invalid = id
		file, e = os.Open(filepath.Join(a.config.MP4Directory, id+".mp4"))
		if e != nil {
			return problem(409, "mp4_missing", "转换缓存不可用，请重新生成")
		}
		info, e := file.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() != status.Size {
			return problem(409, "mp4_corrupt", "转换缓存不完整，请重新生成")
		}
		hash := sha256.New()
		if _, e = io.Copy(hash, file); e != nil {
			return e
		}
		if hex.EncodeToString(hash.Sum(nil)) != status.SHA256 {
			return problem(409, "mp4_corrupt", "转换缓存校验失败")
		}
		if _, e = file.Seek(0, 0); e != nil {
			return e
		}
		invalid = ""
		if e = a.audit.Detail(c, u.ID, "recording.mp4_downloaded", requestID(r), sourceIP(r), map[string]string{"sessionId": v.ID}); e != nil {
			return e
		}
		modified = info.ModTime()
		return nil
	})
	if invalid != "" {
		_, _ = a.db.Exec(r.Context(), `UPDATE jobs SET state='failed',reason='cache_corrupt' WHERE id=$1 AND state='succeeded'`, invalid)
	}
	if e != nil {
		return e
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", `attachment; filename="recording-`+v.ID+`.mp4"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Hour))
	http.ServeContent(w, r, v.ID+".mp4", modified, file)
	return nil
}
