package application

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"next-terminal/backend/internal/modules/audit"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"time"
)

func (a *App) sessionRecording(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	v, u, e := a.inspectSession(r, "audit.read")
	if e != nil {
		return e
	}
	var after int64
	if r.URL.Query().Has("after") {
		after, e = strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if e != nil || after < 0 {
			return fault.Invalid
		}
	}
	page, e := a.audit.Recording(r.Context(), v.ID, after)
	if e != nil {
		return e
	}
	if page.State == "expired" {
		return problem(409, "recording_expired", "会话内容已按保留策略清理，访问摘要仍保留")
	}
	if page.State == "disabled" {
		return problem(409, "recording_disabled", "此会话创建时未启用录屏，访问记录与命令审计仍保留")
	}
	if page.State == "writing" {
		return problem(409, "recording_in_progress", "会话仍在录制，请结束会话后回放")
	}
	if after == 0 && page.Format != audit.MediaFormat {
		if e = a.audit.VerifyRecording(r.Context(), v.ID); e != nil {
			return problem(409, "recording_integrity_failed", "录屏完整性校验失败，已停止回放，请管理员核查")
		}
	}
	if after > page.Total {
		return fault.Invalid
	}
	if after == 0 {
		if e = a.audit.Detail(r.Context(), u.ID, "recording.viewed", requestID(r), sourceIP(r), map[string]string{"sessionId": v.ID}); e != nil {
			return e
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, page)
	return nil
}

func (a *App) recordingSegment(w http.ResponseWriter, r *http.Request) error {
	v, u, e := a.inspectSession(r, "audit.read")
	if e != nil {
		return e
	}
	seq, e := strconv.ParseInt(r.PathValue("seq"), 10, 64)
	if e != nil || seq < 1 || seq > 2048 {
		return fault.Invalid
	}
	select {
	case a.fileSlots <- struct{}{}:
		defer func() { <-a.fileSlots }()
	default:
		return problem(429, "export_busy", "录像读取繁忙，请重试")
	}
	data, e := a.audit.MediaBytes(r.Context(), v.ID, seq)
	if e != nil {
		return e
	}
	name := fmt.Sprintf("recording-%s-%04d.mp4", v.ID, seq)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("download") == "1" {
		if e = a.audit.Detail(r.Context(), u.ID, "recording.downloaded", requestID(r), sourceIP(r), map[string]string{"sessionId": v.ID, "segment": strconv.FormatInt(seq, 10)}); e != nil {
			return e
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	}
	w.Header().Set("Content-Type", "video/mp4")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	return nil
}
