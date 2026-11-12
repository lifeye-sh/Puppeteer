package application

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"next-terminal/backend/internal/modules/audit"
	"time"
)

// JSONL preserves binary output, graphics instructions, resize events and timing.
// It is a versioned original-data archive, not a transcoded video.
func (a *App) downloadRecording(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	r = r.WithContext(ctx)
	v, u, e := a.inspectSession(r, "audit.read")
	if e != nil {
		return e
	}
	select {
	case a.fileSlots <- struct{}{}:
		defer func() { <-a.fileSlots }()
	default:
		return problem(429, "export_busy", "下载繁忙，请稍后重试")
	}
	page, e := a.audit.Recording(ctx, v.ID, 0)
	if e != nil {
		return e
	}
	if page.State == "writing" {
		return problem(409, "recording_in_progress", "录屏尚未结束，请稍后下载")
	}
	if page.Format == audit.MediaFormat {
		return problem(409, "native_media", "请在录屏回放中下载 MP4 分段")
	}
	if page.Total == 0 {
		return problem(409, "recording_empty", "此会话没有录屏内容")
	}
	if e = a.audit.VerifyRecording(ctx, v.ID); e != nil {
		return problem(409, "recording_integrity_failed", "录屏完整性校验失败，不能下载")
	}
	started := false
	e = a.db.Within(ctx, func(c context.Context) error {
		if e := a.audit.LockRecordingExport(c, v.ID); e != nil {
			return e
		}
		if e := a.audit.Detail(c, u.ID, "recording.downloaded", requestID(r), sourceIP(r), map[string]string{"sessionId": v.ID}); e != nil {
			return e
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Disposition", `attachment; filename="recording-`+v.ID+`.ntrec.jsonl"`)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		started = true
		enc := json.NewEncoder(w)
		if e := enc.Encode(map[string]any{"type": "header", "archiveVersion": 1, "sessionId": v.ID, "protocol": v.Protocol, "format": page.Format, "state": page.State, "durationMs": page.DurationMS, "payloadBytes": page.Bytes, "events": page.Total, "payloadEncoding": "base64"}); e != nil {
			return e
		}
		var after int64
		for after < page.Total {
			chunk, e := a.audit.Recording(c, v.ID, after)
			if e != nil {
				return e
			}
			if chunk.Next <= after {
				return problem(409, "recording_incomplete", "录屏数据不完整")
			}
			for _, event := range chunk.Events {
				if e = enc.Encode(event); e != nil {
					return e
				}
			}
			after = chunk.Next
		}
		return enc.Encode(map[string]any{"type": "end", "events": page.Total})
	})
	if started && e != nil {
		slog.Warn("recording export interrupted")
		return nil
	}
	return e
}
