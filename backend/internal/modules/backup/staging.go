package backup

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

// Stage verifies before creating a new private directory. The caller owns the
// parent directory; no archive name can choose an extraction destination.
// The second pass rechecks content, protecting against source modification.
func Stage(src io.ReadSeeker, identity string, maxSchema int, parent string) (m Manifest, dir string, err error) {
	m, err = InspectArchive(src, identity, maxSchema)
	if err != nil {
		return
	}
	if _, err = src.Seek(0, io.SeekStart); err != nil {
		return
	}
	key, _ := age.ParseX25519Identity(identity)
	decrypted, e := age.Decrypt(src, key)
	if e != nil {
		return m, "", e
	}
	dir, err = os.MkdirTemp(parent, "nt-restore-")
	if err != nil {
		return
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(dir)
			dir = ""
		}
	}()
	tr := tar.NewReader(decrypted)
	if _, err = tr.Next(); err != nil {
		return
	}
	for _, entry := range m.Entries {
		var h *tar.Header
		h, err = tr.Next()
		if err != nil {
			return
		}
		if h.Name != entry.Path || h.Size != entry.Size || h.Typeflag != tar.TypeReg {
			return m, dir, errors.New("archive changed between validation and staging")
		}
		dst := filepath.Join(dir, filepath.FromSlash(entry.Path))
		if !strings.HasPrefix(dst, dir+string(os.PathSeparator)) {
			return m, dir, errors.New("invalid staging path")
		}
		if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return
		}
		var f *os.File
		f, err = os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return
		}
		hash := sha256.New()
		var n int64
		n, err = io.Copy(io.MultiWriter(f, hash), tr)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return
		}
		if n != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return m, dir, errors.New("staged payload digest mismatch")
		}
	}
	// Reauthenticate this pass completely before allowing any consumer access.
	if _, err = src.Seek(0, io.SeekStart); err != nil {
		return
	}
	if _, err = InspectArchive(src, identity, maxSchema); err != nil {
		return
	}
	success = true
	return
}
