package backup

import (
	"context"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"os"
	"path/filepath"
	"time"
)

// CreateSnapshot requires a drained source and private workspace. Keys, object
// inventory and dump are verified against one exported repeatable-read snapshot.
func CreateSnapshot(ctx context.Context, dsn, parent, output, recipient, id, version, scope string, keys RecoveryBundle) (Manifest, error) {
	var m Manifest
	if scope != "full" && scope != "control-plane" {
		return m, errors.New("invalid backup scope")
	}
	if _, e := age.ParseX25519Recipient(recipient); e != nil {
		return m, errors.New("invalid recovery recipient")
	}
	dir, e := os.MkdirTemp(parent, "backup-snapshot-")
	if e != nil {
		return m, e
	}
	defer os.RemoveAll(dir)
	if e = os.Mkdir(filepath.Join(dir, "keys"), 0700); e != nil {
		return m, e
	}
	if e = os.Mkdir(filepath.Join(dir, "objects"), 0700); e != nil {
		return m, e
	}
	_, e = dumpSnapshot(ctx, dsn, dir, func(tx pgx.Tx) error {
		m = Manifest{Format: "nt-backup", FormatVersion: 1, Product: "nt-self-hosted", BackupID: id, CreatedAt: time.Now().UTC(), AppVersion: version, PGMajor: 18, DumpToolMajor: 18, Scope: scope, Includes: []string{"configuration", "audit-metadata", "encrypted-credentials", "CA", "recovery-keys", "object-manifest"}, Excludes: []string{"external-host-files", "previous-backup-archives", "external-WAL-repository", "derived-mp4-cache"}}
		if e := tx.QueryRow(ctx, "SELECT instance_id::text,schema_version FROM instance_state WHERE singleton=1").Scan(&m.InstanceID, &m.SchemaVersion); e != nil {
			return e
		}
		if m.SchemaVersion != SupportedSchema {
			return errors.New("source schema not supported")
		}
		var databaseBytes uint64
		if e := tx.QueryRow(ctx, "SELECT pg_database_size(current_database())").Scan(&databaseBytes); e != nil {
			return e
		}
		if e := RequireSpace(ctx, dir, databaseBytes*3+(256<<20)); e != nil {
			return e
		}
		if e := keys.Verify(ctx, tx.Conn()); e != nil {
			return e
		}
		raw, e := json.Marshal(keys)
		if e != nil {
			return e
		}
		defer clear(raw)
		if e = os.WriteFile(filepath.Join(dir, "keys", "recovery-bundle.json"), raw, 0600); e != nil {
			return e
		}
		f, e := os.OpenFile(filepath.Join(dir, "objects", "manifest.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		e = objectManifest(ctx, tx, f, scope == "full")
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e == nil {
			e = ce
		}
		if scope == "full" {
			m.Includes = append(m.Includes, "postgres-object-payloads")
		} else {
			m.Excludes = append(m.Excludes, "recording-payloads", "storage-payloads", "staged-upload-payloads")
		}
		return e
	}, scope == "control-plane")
	if e != nil {
		return m, e
	}
	return PackDirectory(dir, output, recipient, m)
}
