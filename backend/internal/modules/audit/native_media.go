package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"next-terminal/backend/internal/platform/fault"
)

const MediaFormat = "native-rdp-mp4-v1"
const MaxMediaSegment = 16 << 20

type MediaSegment struct {
	Sequence   int64 `json:"sequence"`
	StartMS    int64 `json:"startMs"`
	DurationMS int64 `json:"durationMs"`
	Bytes      int64 `json:"bytes"`
}

func (s *Service) BeginMedia(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `UPDATE recordings SET format=$2 WHERE session_id=$1 AND state='writing' AND events=0`, id, MediaFormat)
	if err == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return err
}

func (s *Service) AppendMedia(ctx context.Context, id string, seq int, start, duration int64, digest string, data []byte) error {
	sum := sha256.Sum256(data)
	if seq < 1 || seq > 2048 || start < 0 || duration <= 0 || duration > 10000 || len(data) == 0 || len(data) > MaxMediaSegment || hex.EncodeToString(sum[:]) != digest {
		return fault.Invalid
	}
	return s.db.Within(ctx, func(c context.Context) error {
		enabled, e := s.RecordingEnabled(c, id)
		if e != nil {
			return e
		}
		if !enabled {
			return nil
		}
		tag, err := s.db.Exec(c, `UPDATE recordings SET bytes=bytes+$4,events=$2 WHERE session_id=$1 AND format=$3 AND state='writing' AND events=$2::bigint-1
 AND COALESCE((SELECT elapsed_ms FROM recording_chunks WHERE session_id=$1 AND sequence=$2::bigint-1),0)=$5`, id, int64(seq), MediaFormat, len(data), start)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fault.Conflict
		}
		_, err = s.db.Exec(c, `INSERT INTO recording_chunks(session_id,sequence,elapsed_ms,kind,payload,media_sha256) VALUES($1,$2,$3,'mp4',$4,$5)`, id, seq, start+duration, data, sum[:])
		return err
	})
}

func (s *Service) mediaIndex(ctx context.Context, id string, v *RecordingPage) error {
	rows, err := s.db.Rows(ctx, `SELECT sequence,elapsed_ms,octet_length(payload) FROM recording_chunks WHERE session_id=$1 AND kind='mp4' ORDER BY sequence LIMIT 2049`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	var end, totalBytes int64
	for rows.Next() {
		segment := MediaSegment{StartMS: end}
		if err = rows.Scan(&segment.Sequence, &end, &segment.Bytes); err != nil {
			return err
		}
		segment.DurationMS = end - segment.StartMS
		if segment.Sequence != int64(len(v.Segments)+1) || segment.Sequence > 2048 || segment.DurationMS <= 0 || segment.DurationMS > 10000 {
			return fault.Conflict
		}
		v.Segments = append(v.Segments, segment)
		totalBytes += segment.Bytes
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if int64(len(v.Segments)) != v.Total || totalBytes != v.Bytes || end != v.DurationMS {
		return fault.Conflict
	}
	return nil
}

func (s *Service) MediaBytes(ctx context.Context, id string, seq int64) ([]byte, error) {
	var data, digest []byte
	err := s.db.Row(ctx, `SELECT c.payload,c.media_sha256 FROM recording_chunks c JOIN recordings r USING(session_id)
 WHERE c.session_id=$1 AND c.sequence=$2 AND c.kind='mp4' AND r.format=$3 AND r.enabled AND r.state IN ('complete','interrupted') AND r.integrity_state<>'invalid'`, id, seq, MediaFormat).Scan(&data, &digest)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if !bytes.Equal(sum[:], digest) {
		return nil, fault.Conflict
	}
	return data, nil
}
