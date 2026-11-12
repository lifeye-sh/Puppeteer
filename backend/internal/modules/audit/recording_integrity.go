package audit

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"next-terminal/backend/internal/platform/fault"
)

// The digest covers ordered sequence/time/kind/payload, not just printable text.
// It detects accidental corruption; it is not an externally signed audit proof.
func (s *Service) sealRecording(ctx context.Context, id string) error {
	var state, stored, integrity string
	var count, size int64
	if e := s.db.Row(ctx, `SELECT state,events,bytes,sha256,integrity_state FROM recordings WHERE session_id=$1 FOR UPDATE`, id).Scan(&state, &count, &size, &stored, &integrity); e != nil {
		return e
	}
	if state == "writing" {
		return fault.Conflict
	}
	rows, e := s.db.Rows(ctx, `SELECT sequence,elapsed_ms,kind,payload FROM recording_chunks WHERE session_id=$1 ORDER BY sequence`, id)
	if e != nil {
		return e
	}
	h := sha256.New()
	var seen, bytes int64
	valid := true
	for rows.Next() {
		var sequence, ms int64
		var kind string
		var payload []byte
		if e = rows.Scan(&sequence, &ms, &kind, &payload); e != nil {
			rows.Close()
			return e
		}
		seen++
		bytes += int64(len(payload))
		if sequence != seen || ms < 0 {
			valid = false
		}
		for _, v := range []int64{sequence, ms, int64(len(kind)), int64(len(payload))} {
			_ = binary.Write(h, binary.BigEndian, v)
		}
		_, _ = h.Write([]byte(kind))
		_, _ = h.Write(payload)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if !valid || seen != count || bytes != size || (stored != "" && stored != digest) || integrity == "invalid" {
		_, e = s.db.Exec(ctx, `UPDATE recordings SET integrity_state='invalid' WHERE session_id=$1`, id)
		return e
	}
	_, e = s.db.Exec(ctx, `UPDATE recordings SET sha256=$2,sealed_at=COALESCE(sealed_at,now()),integrity_state=CASE WHEN sha256='' THEN 'sealed' ELSE integrity_state END WHERE session_id=$1`, id, digest)
	return e
}
func (s *Service) VerifyRecording(ctx context.Context, id string) error {
	if e := s.db.Within(ctx, func(ctx context.Context) error { return s.sealRecording(ctx, id) }); e != nil {
		return e
	}
	var state string
	if e := s.db.Row(ctx, `SELECT integrity_state FROM recordings WHERE session_id=$1`, id).Scan(&state); e != nil {
		return e
	}
	if state != "sealed" {
		return fault.Conflict
	}
	return nil
}

// VerifyRecordingLocked is used inside the caller's snapshot transaction.
func (s *Service) VerifyRecordingLocked(ctx context.Context, id string) error {
	if e := s.sealRecording(ctx, id); e != nil {
		return e
	}
	var state string
	if e := s.db.Row(ctx, `SELECT integrity_state FROM recordings WHERE session_id=$1`, id).Scan(&state); e != nil {
		return e
	}
	if state != "sealed" {
		return fault.Conflict
	}
	return nil
}
func (s *Service) RetentionPreview(ctx context.Context, days int) (map[string]any, error) {
	var count, bytes, held int64
	e := s.db.Row(ctx, `SELECT count(*) FILTER(WHERE h.record_id IS NULL),COALESCE(sum(r.bytes) FILTER(WHERE h.record_id IS NULL),0),count(*) FILTER(WHERE h.record_id IS NOT NULL) FROM recordings r JOIN sessions s ON s.id=r.session_id LEFT JOIN audit_holds h ON h.category='access' AND h.record_id=r.session_id WHERE r.state<>'writing' AND s.audit_purged_at IS NULL AND s.closed_at<now()-($1*interval '1 day') AND $1>0`, days).Scan(&count, &bytes, &held)
	return map[string]any{"days": days, "recordings": count, "bytes": bytes, "held": held, "previewOnly": true}, e
}
