package agentupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// canonical resolves existing ancestors as well as paths that will be created.
func canonical(path string) (string, error) {
	path, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e == nil {
		return resolved, nil
	}
	if !os.IsNotExist(e) {
		return "", e
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", e
	}
	resolved, e = canonical(parent)
	if e != nil {
		return "", e
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}
func separateDirectories(a, b string) error {
	a, e := canonical(a)
	if e != nil {
		return e
	}
	b, e = canonical(b)
	if e != nil {
		return e
	}
	within := func(parent, child string) bool {
		r, e := filepath.Rel(parent, child)
		return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
	}
	if within(a, b) || within(b, a) {
		return errors.New("public repository and private/output directories must be separate")
	}
	return nil
}

// ExportRepository exports an allowlist, never arbitrary files or signing keys.
// The new directory is promoted by the operator only after successful return.
func ExportRepository(repo, output string) (err error) {
	if err = separateDirectories(repo, output); err != nil {
		return err
	}
	roots, root, e := rootChain(repo)
	if e != nil {
		return e
	}
	names := append(roots, "targets.json", "snapshot.json", "timestamp.json", "targets/nt-agent-linux-amd64")
	data := map[string][]byte{}
	for _, name := range names {
		path := filepath.Join(repo, filepath.FromSlash(name))
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		limit := int64(4 << 20)
		if strings.HasPrefix(name, "targets/") {
			limit = 128 << 20
		}
		if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > limit {
			return errors.New("invalid public repository file")
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		data[name] = raw
	}
	targets, e := metadata.Targets().FromBytes(data["targets.json"])
	if e != nil {
		return e
	}
	snapshot, e := metadata.Snapshot().FromBytes(data["snapshot.json"])
	if e != nil {
		return e
	}
	timestamp, e := metadata.Timestamp().FromBytes(data["timestamp.json"])
	if e != nil {
		return e
	}
	for role, value := range map[string]any{"root": root, "targets": targets, "snapshot": snapshot, "timestamp": timestamp} {
		if e = root.VerifyDelegate(role, value); e != nil {
			return errors.New("invalid release signature: " + role)
		}
	}
	now := time.Now()
	for _, expires := range []time.Time{root.Signed.Expires, targets.Signed.Expires, snapshot.Signed.Expires, timestamp.Signed.Expires} {
		if !expires.After(now) {
			return errors.New("release metadata expired")
		}
	}
	sm, tm := snapshot.Signed.Meta["targets.json"], timestamp.Signed.Meta["snapshot.json"]
	if sm == nil || tm == nil || sm.Version != targets.Signed.Version || tm.Version != snapshot.Signed.Version {
		return errors.New("release metadata versions differ")
	}
	target := targets.Signed.Targets["nt-agent-linux-amd64"]
	if target == nil || len(targets.Signed.Targets) != 1 {
		return errors.New("unexpected release targets")
	}
	raw := data["targets/nt-agent-linux-amd64"]
	digest := sha256.Sum256(raw)
	if target.Length != int64(len(raw)) || hex.EncodeToString(target.Hashes["sha256"]) != hex.EncodeToString(digest[:]) {
		return errors.New("release target digest mismatch")
	}
	var release Release
	if target.Custom == nil || json.Unmarshal(*target.Custom, &release) != nil || release.Sequence != targets.Signed.Version || release.OS != "linux" || release.Arch != "amd64" {
		return errors.New("invalid release identity")
	}
	if err = os.Mkdir(output, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(output)
		}
	}()
	if err = os.Mkdir(filepath.Join(output, "targets"), 0755); err != nil {
		return err
	}
	for _, name := range names {
		if err = os.WriteFile(filepath.Join(output, filepath.FromSlash(name)), data[name], 0444); err != nil {
			return err
		}
	}
	return os.Chmod(output, 0755)
}
