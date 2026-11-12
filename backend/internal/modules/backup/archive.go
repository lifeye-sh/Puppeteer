// Package backup implements the versioned, encrypted restore boundary.
package backup

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"filippo.io/age"
)

const MaxManifest = 1 << 20
const MaxArchive int64 = 100 << 30
const SupportedSchema = 103

type Entry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Format        string    `json:"format"`
	FormatVersion int       `json:"formatVersion"`
	Product       string    `json:"product"`
	BackupID      string    `json:"backupId"`
	InstanceID    string    `json:"instanceId"`
	CreatedAt     time.Time `json:"createdAt"`
	AppVersion    string    `json:"appVersion"`
	SchemaVersion int       `json:"schemaVersion"`
	PGMajor       int       `json:"pgMajor"`
	DumpToolMajor int       `json:"dumpToolMajor"`
	Scope         string    `json:"scope"`
	Includes      []string  `json:"includes"`
	Excludes      []string  `json:"excludes"`
	Entries       []Entry   `json:"entries"`
}

func (m Manifest) Validate(maxSchema int) error {
	if m.Format != "nt-backup" || m.FormatVersion != 1 || m.Product != "nt-self-hosted" || m.PGMajor != 18 || m.DumpToolMajor != 18 || m.SchemaVersion < 1 || m.SchemaVersion > maxSchema || (m.Scope != "control-plane" && m.Scope != "full") {
		return errors.New("unsupported archive version or scope")
	}
	if m.BackupID == "" || m.InstanceID == "" || m.CreatedAt.IsZero() || m.AppVersion == "" || len(m.Includes) == 0 || len(m.Excludes) == 0 {
		return errors.New("incomplete archive manifest")
	}
	if len(m.Entries) < 2 || len(m.Entries) > 10000 {
		return errors.New("invalid entry count")
	}
	seen := map[string]bool{}
	var total int64
	for _, e := range m.Entries {
		// Expand only after adding a contributor with an explicit format contract.
		if e.Path != "database.dump" && e.Path != "keys/recovery-bundle.json" && e.Path != "objects/manifest.json" {
			return errors.New("unsupported archive path")
		}
		if seen[e.Path] || e.Size < 0 || e.Size > MaxArchive-total {
			return errors.New("duplicate entry or archive too large")
		}
		seen[e.Path] = true
		if e.Path == "keys/recovery-bundle.json" && e.Size > 16384 {
			return errors.New("recovery bundle too large")
		}
		if e.Path == "objects/manifest.json" && e.Size > 256<<20 {
			return errors.New("object manifest exceeds limit")
		}
		total += e.Size
		digest, err := hex.DecodeString(e.SHA256)
		if err != nil || len(digest) != sha256.Size {
			return errors.New("invalid entry digest")
		}
	}
	if !seen["database.dump"] || !seen["keys/recovery-bundle.json"] {
		return errors.New("database or recovery keys missing")
	}
	if m.Scope == "full" && !seen["objects/manifest.json"] {
		return errors.New("full backup object manifest missing")
	}
	return nil
}

// WriteArchive streams pre-hashed immutable inputs. Callers publish output only
// after this function succeeds; failed output must be discarded.
func WriteArchive(dst io.Writer, m Manifest, recipient string, open func(string) (io.ReadCloser, error)) error {
	if err := m.Validate(m.SchemaVersion); err != nil {
		return err
	}
	public, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return errors.New("invalid recovery recipient")
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(raw) > MaxManifest {
		return errors.New("manifest too large")
	}
	encrypted, err := age.Encrypt(dst, public)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(encrypted)
	if err = tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(raw)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err = tw.Write(raw); err != nil {
		return err
	}
	for _, entry := range m.Entries {
		r, e := open(entry.Path)
		if e != nil {
			return e
		}
		e = tw.WriteHeader(&tar.Header{Name: entry.Path, Mode: 0600, Size: entry.Size, Typeflag: tar.TypeReg})
		h := sha256.New()
		var n int64
		if e == nil {
			n, e = io.Copy(io.MultiWriter(tw, h), io.LimitReader(r, entry.Size+1))
		}
		closeErr := r.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		if n != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
			return errors.New("source changed during backup")
		}
	}
	if err = tw.Close(); err != nil {
		return err
	}
	return encrypted.Close()
}

// InspectArchive verifies the complete encrypted stream, without extracting
// files or executing SQL. A manifest is returned only after every digest passes.
func InspectArchive(src io.Reader, identity string, maxSchema int) (Manifest, error) {
	var empty Manifest
	key, err := age.ParseX25519Identity(identity)
	if err != nil {
		return empty, errors.New("invalid recovery identity")
	}
	decrypted, err := age.Decrypt(src, key)
	if err != nil {
		return empty, errors.New("cannot decrypt archive")
	}
	// Include bounded tar headers/padding in addition to payload limits.
	limited := &io.LimitedReader{R: decrypted, N: MaxArchive + 12*1024*1024}
	tr := tar.NewReader(limited)
	first, err := tr.Next()
	if err != nil || first.Name != "manifest.json" || first.Typeflag != tar.TypeReg || first.Size < 1 || first.Size > MaxManifest {
		return empty, errors.New("invalid first manifest entry")
	}
	raw, err := io.ReadAll(tr)
	if err != nil {
		return empty, err
	}
	var m Manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return empty, errors.New("invalid manifest JSON")
	}
	if err = m.Validate(maxSchema); err != nil {
		return empty, err
	}
	for _, entry := range m.Entries {
		header, e := tr.Next()
		if e != nil {
			return empty, errors.New("archive entry missing")
		}
		if header.Typeflag != tar.TypeReg || header.Name != entry.Path || header.Size != entry.Size || header.Linkname != "" {
			return empty, errors.New("archive entry mismatch")
		}
		h := sha256.New()
		n, e := io.Copy(h, tr)
		if e != nil {
			return empty, e
		}
		if n != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
			return empty, fmt.Errorf("digest mismatch: %s", entry.Path)
		}
	}
	if _, err = tr.Next(); err != io.EOF {
		return empty, errors.New("unexpected trailing archive entry")
	}
	// Reading past tar EOF is essential: age authenticates its final chunk only
	// when the underlying stream is consumed completely. Reject trailing data.
	tail := make([]byte, 4096)
	for {
		n, e := limited.Read(tail)
		for _, b := range tail[:n] {
			if b != 0 {
				return empty, errors.New("trailing archive data")
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return empty, errors.New("encrypted archive integrity failed")
		}
	}
	if limited.N == 0 {
		return empty, errors.New("archive exceeds limit")
	}
	return m, nil
}
