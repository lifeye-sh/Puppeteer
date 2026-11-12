package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var payloadTables = []struct{ name, key, data string }{
	{"recording_chunks", "session_id::text||':'||sequence::text", "payload"},
	{"storage_chunks", "node_id::text||':'||chunk_index::text", "data"},
	{"upload_chunks", "task_id::text||':'||offset_bytes::text", "payload"},
	{"storage_upload_parts", "upload_id::text||':'||chunk_index::text", "data"},
}

type objectQuery interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// Object payloads currently live in PostgreSQL chunks. Full scope includes them
// in the same pg_dump snapshot, with an independently verified ordered manifest.
func objectManifest(ctx context.Context, q objectQuery, w io.Writer, included bool) error {
	return objectManifestSelected(ctx, q, w, included, false)
}
func objectManifestSelected(ctx context.Context, q objectQuery, w io.Writer, included, permanent bool) error {
	if _, e := fmt.Fprintf(w, `{"version":1,"backend":"postgres-chunks-v1","included":%t,"objects":[`, included); e != nil {
		return e
	}
	first := true
	for _, t := range payloadTables {
		if permanent && (t.name == "upload_chunks" || t.name == "storage_upload_parts") {
			continue
		}
		rows, e := q.Query(ctx, "SELECT "+t.key+",octet_length("+t.data+"),encode(sha256("+t.data+"),'hex') FROM "+t.name+" ORDER BY ("+t.key+") COLLATE \"C\"")
		if e != nil {
			return e
		}
		for rows.Next() {
			var key, digest string
			var size int64
			if e = rows.Scan(&key, &size, &digest); e != nil {
				rows.Close()
				return e
			}
			if !first {
				if _, e = io.WriteString(w, ","); e != nil {
					rows.Close()
					return e
				}
			}
			first = false
			if e = json.NewEncoder(w).Encode(Entry{Path: t.name + "/" + key, Size: size, SHA256: digest}); e != nil {
				rows.Close()
				return e
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	_, e := io.WriteString(w, "]}")
	return e
}

// After quarantine, unfinished upload buffers have intentionally been removed.
// Resume still verifies every persistent recording and shared-drive chunk.
func VerifyPermanentObjects(ctx context.Context, c *pgx.Conn, dir, scope string) error {
	if scope == "control-plane" {
		return VerifyObjects(ctx, c, dir, scope)
	}
	f, e := os.Open(filepath.Join(dir, "objects", "manifest.json"))
	if e != nil {
		return e
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 256<<20))
	if token, e := d.Token(); e != nil || token != json.Delim('{') {
		return errors.New("invalid object manifest")
	}
	expected := sha256.New()
	io.WriteString(expected, `{"version":1,"backend":"postgres-chunks-v1","included":true,"objects":[`)
	first := true
	seen := false
	for d.More() {
		token, e := d.Token()
		if e != nil {
			return e
		}
		name, ok := token.(string)
		if !ok {
			return errors.New("invalid object manifest field")
		}
		if name != "objects" {
			var ignored json.RawMessage
			if e = d.Decode(&ignored); e != nil {
				return e
			}
			continue
		}
		if seen {
			return errors.New("duplicate object manifest")
		}
		seen = true
		if token, e = d.Token(); e != nil || token != json.Delim('[') {
			return errors.New("invalid object manifest array")
		}
		for d.More() {
			var item Entry
			if e = d.Decode(&item); e != nil {
				return e
			}
			if strings.HasPrefix(item.Path, "upload_chunks/") || strings.HasPrefix(item.Path, "storage_upload_parts/") {
				continue
			}
			if !strings.HasPrefix(item.Path, "recording_chunks/") && !strings.HasPrefix(item.Path, "storage_chunks/") {
				return errors.New("unsupported persistent object")
			}
			if !first {
				io.WriteString(expected, ",")
			}
			first = false
			if e = json.NewEncoder(expected).Encode(item); e != nil {
				return e
			}
		}
		if _, e = d.Token(); e != nil {
			return e
		}
	}
	if !seen {
		return errors.New("object inventory missing")
	}
	if _, e = d.Token(); e != nil {
		return e
	}
	io.WriteString(expected, "]}")
	actual := sha256.New()
	if e = objectManifestSelected(ctx, c, actual, true, true); e != nil {
		return e
	}
	if !bytes.Equal(expected.Sum(nil), actual.Sum(nil)) {
		return errors.New("persistent object verification failed")
	}
	return nil
}
func VerifyObjects(ctx context.Context, c *pgx.Conn, dir, scope string) error {
	if scope == "control-plane" {
		for _, t := range payloadTables {
			var n int
			if e := c.QueryRow(ctx, "SELECT count(*) FROM "+t.name).Scan(&n); e != nil {
				return e
			}
			if n != 0 {
				return errors.New("control-plane archive contains excluded payloads")
			}
		}
		return nil
	}
	f, e := os.Open(filepath.Join(dir, "objects", "manifest.json"))
	if e != nil {
		return e
	}
	defer f.Close()
	expected := sha256.New()
	if _, e = io.Copy(expected, f); e != nil {
		return e
	}
	actual := sha256.New()
	if e = objectManifest(ctx, c, actual, true); e != nil {
		return e
	}
	if !bytes.Equal(expected.Sum(nil), actual.Sum(nil)) {
		return errors.New("restored object manifest mismatch")
	}
	return nil
}
