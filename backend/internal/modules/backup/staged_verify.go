package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func VerifyStagedFiles(dir string, m Manifest) error {
	if e := m.Validate(SupportedSchema); e != nil {
		return e
	}
	for _, entry := range m.Entries {
		path := filepath.Join(dir, filepath.FromSlash(entry.Path))
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() || info.Size() != entry.Size {
			return errors.New("staged archive file changed")
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		f.Close()
		if e != nil || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
			return errors.New("staged archive integrity failed")
		}
	}
	return nil
}
