package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// PackDirectory publishes a new encrypted file only after all input hashes have
// been verified. It never overwrites an existing backup, even under concurrent
// calls. Only allow-listed files in a private, caller-owned staging directory
// may be included; the directory must not be shared with untrusted writers.
func PackDirectory(dir, output, recipient string, m Manifest) (Manifest, error) {
	paths := []string{"database.dump", "keys/recovery-bundle.json"}
	if _, e := os.Lstat(filepath.Join(dir, "objects", "manifest.json")); e == nil {
		paths = append(paths, "objects/manifest.json")
	} else if !os.IsNotExist(e) {
		return m, e
	}
	m.Entries = nil
	open := func(name string) (io.ReadCloser, error) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		info, e := os.Lstat(p)
		if e != nil {
			return nil, e
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("backup inputs must be regular files")
		}
		return os.Open(p)
	}
	for _, p := range paths {
		r, e := open(p)
		if e != nil {
			return m, e
		}
		h := sha256.New()
		n, e := io.Copy(h, io.LimitReader(r, MaxArchive+1))
		r.Close()
		if e != nil {
			return m, e
		}
		if n > MaxArchive {
			return m, errors.New("archive input exceeds limit")
		}
		m.Entries = append(m.Entries, Entry{p, n, hex.EncodeToString(h.Sum(nil))})
	}
	if e := m.Validate(m.SchemaVersion); e != nil {
		return m, e
	}
	temp, e := os.CreateTemp(filepath.Dir(output), ".backup-attempt-")
	if e != nil {
		return m, e
	}
	defer os.Remove(temp.Name())
	if e = WriteArchive(temp, m, recipient, open); e == nil {
		e = temp.Sync()
	}
	closeErr := temp.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return m, e
	}
	if e = os.Link(temp.Name(), output); e != nil {
		return m, e
	}
	directory, e := os.Open(filepath.Dir(output))
	if e != nil {
		return m, e
	}
	defer directory.Close()
	return m, directory.Sync()
}
