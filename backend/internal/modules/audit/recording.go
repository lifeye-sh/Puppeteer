package audit

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
)

// Payload is encoded as base64 by encoding/json, preserving split UTF-8 and ANSI bytes.
type RecordingEvent struct {
	Sequence  int64  `json:"sequence"`
	ElapsedMS int64  `json:"elapsedMs"`
	Kind      string `json:"kind"`
	Payload   []byte `json:"payload"`
}

type RecordingPage struct {
	Format     string           `json:"format"`
	State      string           `json:"state"`
	Bytes      int64            `json:"bytes"`
	Total      int64            `json:"total"`
	DurationMS int64            `json:"durationMs"`
	Next       int64            `json:"next"`
	Events     []RecordingEvent `json:"events"`
	Segments   []MediaSegment   `json:"segments,omitempty"`
}

func (s *Service) Recording(ctx context.Context, id string, after int64) (v RecordingPage, err error) {
	return s.recording(ctx, id, after, false)
}

// Observe reads only the durable prefix. The application checks a bound observer lease.
func (s *Service) Observe(ctx context.Context, id string, after int64) (RecordingPage, error) {
	return s.recording(ctx, id, after, true)
}
func (s *Service) recording(ctx context.Context, id string, after int64, live bool) (v RecordingPage, err error) {
	err = s.db.Row(ctx, `SELECT format,CASE WHEN state='expired' THEN 'expired' WHEN enabled THEN state ELSE 'disabled' END,bytes,events,COALESCE((SELECT max(elapsed_ms) FROM recording_chunks WHERE session_id=$1),0) FROM recordings WHERE session_id=$1`, id).Scan(&v.Format, &v.State, &v.Bytes, &v.Total, &v.DurationMS)
	if err != nil {
		return
	}
	if v.State == "disabled" || v.State == "expired" {
		if live {
			return v, fault.Denied
		}
		v.Events = []RecordingEvent{}
		return
	}
	v.Next = after
	v.Events = []RecordingEvent{}
	// A writing recording is not a stable replay. The HTTP handler reports 409.
	if v.State == "writing" && !live {
		return
	}
	if v.Format == MediaFormat {
		if live || after != 0 {
			return v, fault.Invalid
		}
		err = s.mediaIndex(ctx, id, &v)
		return v, err
	}
	rows, e := s.db.Rows(ctx, `SELECT sequence,elapsed_ms,kind,payload FROM recording_chunks WHERE session_id=$1 AND sequence>$2 AND sequence<=$3 ORDER BY sequence LIMIT 64`, id, after, v.Total)
	if e != nil {
		return v, e
	}
	defer rows.Close()
	for rows.Next() {
		var event RecordingEvent
		if e = rows.Scan(&event.Sequence, &event.ElapsedMS, &event.Kind, &event.Payload); e != nil {
			return v, e
		}
		v.Events = append(v.Events, event)
		v.Next = event.Sequence
	}
	return v, rows.Err()
}
