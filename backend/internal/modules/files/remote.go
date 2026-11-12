package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/pkg/sftp"
	"io"
	"next-terminal/backend/internal/platform/fault"
	"os"
	"path"
	"strings"
	"unicode/utf8"
)

func Normalize(value string) (string, error) {
	if len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\\x00\r\n") {
		return "", fault.Invalid
	}
	parts := strings.Split(strings.TrimPrefix(value, "/"), "/")
	if len(parts) > 32 {
		return "", fault.Invalid
	}
	for _, p := range parts {
		if p == ".." || strings.HasPrefix(p, ".nt-upload-") {
			return "", fault.Invalid
		}
	}
	return strings.TrimPrefix(path.Clean("/"+value), "/"), nil
}

type Remote struct {
	Client *sftp.Client
	Root   string
}

// Refuse symlinks in every traversed component, including the leaf. RealPath is
// defense in depth; strong isolation against remote concurrent changes needs chroot.
func (r Remote) Resolve(value string, missing bool) (string, error) {
	rel, e := Normalize(value)
	if e != nil {
		return "", e
	}
	canonical, e := r.Client.RealPath(r.Root)
	if e != nil {
		return "", e
	}
	if canonical != r.Root {
		return "", fault.Denied
	}
	current := r.Root
	if rel != "" {
		parts := strings.Split(rel, "/")
		for i, p := range parts {
			current = path.Join(current, p)
			info, e := r.Client.Lstat(current)
			if e != nil {
				if missing && i == len(parts)-1 && os.IsNotExist(e) {
					return current, nil
				}
				return "", e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fault.Denied
			}
			if i < len(parts)-1 && !info.IsDir() {
				return "", fault.Invalid
			}
		}
	}
	canonical, e = r.Client.RealPath(current)
	if e != nil {
		return "", e
	}
	if canonical != current || !(current == r.Root || strings.HasPrefix(current, strings.TrimRight(r.Root, "/")+"/")) {
		return "", fault.Denied
	}
	return current, nil
}

type Entry struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Directory bool   `json:"directory"`
	Symlink   bool   `json:"symlink"`
	Mode      string `json:"mode"`
	Modified  int64  `json:"modified"`
}

func (r Remote) List(ctx context.Context, value string) ([]Entry, error) {
	full, e := r.Resolve(value, false)
	if e != nil {
		return nil, e
	}
	items, e := r.Client.ReadDirContext(ctx, full)
	if e != nil {
		return nil, e
	}
	if len(items) > 2000 {
		return nil, fault.Conflict
	}
	out := []Entry{}
	for _, info := range items {
		if strings.HasPrefix(info.Name(), ".nt-upload-") {
			continue
		}
		out = append(out, Entry{info.Name(), info.Size(), info.IsDir(), info.Mode()&os.ModeSymlink != 0, info.Mode().String(), info.ModTime().Unix()})
	}
	return out, nil
}
func Hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func (r Remote) Read(value string, limit int64) ([]byte, error) {
	full, e := r.Resolve(value, false)
	if e != nil {
		return nil, e
	}
	return r.ReadAbsolute(full, limit)
}
func (r Remote) ReadAbsolute(full string, limit int64) ([]byte, error) {
	f, e := r.Client.Open(full)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fault.Invalid
	}
	data, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(data)) > limit {
		return nil, fault.Invalid
	}
	return data, nil
}
func (r Remote) CheckTarget(value, expected string) (string, error) {
	rel, e := Normalize(value)
	if e != nil || rel == "" {
		return "", fault.Invalid
	}
	full, e := r.Resolve(rel, true)
	if e != nil {
		return "", e
	}
	info, e := r.Client.Lstat(full)
	if os.IsNotExist(e) {
		if expected != "" {
			return "", fault.Conflict
		}
		return full, nil
	}
	if e != nil {
		return "", e
	}
	if expected == "" || !info.Mode().IsRegular() {
		return "", fault.Conflict
	}
	data, e := r.ReadAbsolute(full, MaxFile)
	if e != nil {
		return "", e
	}
	if Hash(data) != expected {
		return "", fault.Conflict
	}
	return full, nil
}
func TempName(full, id string) string { return path.Join(path.Dir(full), ".nt-upload-"+id) }
