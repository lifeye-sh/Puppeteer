package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"strings"
)

// Resume only operates on the random, immutable key owned by the task ledger.
// A previously published object must match; an unexpected final is never replaced.
func ResumeCopyRemote(ctx context.Context, d Destination, local, key, digest string) (int64, error) {
	if key == "" || path.Base(key) != key || key == "." || strings.ContainsAny(key, "\\\x00") {
		return 0, errors.New("invalid owned remote key")
	}
	info, e := os.Stat(local)
	if e != nil {
		return 0, e
	}
	r, e := openRemote(ctx, d)
	if e != nil {
		return 0, e
	}
	defer r.Close()
	existing, e := r.Open(ctx, key)
	if e == nil {
		h := sha256.New()
		n, e := io.Copy(h, io.LimitReader(existing, info.Size()+1))
		existing.Close()
		if e != nil || n != info.Size() || hex.EncodeToString(h.Sum(nil)) != digest {
			return 0, errors.New("existing remote backup digest mismatch")
		}
		return n, nil
	}
	if e = r.Remove(ctx, key+".partial"); e != nil {
		return 0, errors.New("unfinished remote copy cleanup failed")
	}
	return CopyRemote(ctx, d, local, key, digest)
}
