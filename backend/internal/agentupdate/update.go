package agentupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	tufconfig "github.com/theupdateframework/go-tuf/v2/metadata/config"
	"github.com/theupdateframework/go-tuf/v2/metadata/updater"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var ErrNoUpdate = errors.New("already up to date")

type Config struct {
	Repository           string `json:"repository"`
	RootFile             string `json:"rootFile"`
	StateDir             string `json:"stateDir"`
	AutoInstall          bool   `json:"autoInstall"`
	AllowHTTPDevelopment bool   `json:"allowHttpDevelopment"`
}
type Release struct {
	Sequence int64  `json:"sequence"`
	Version  string `json:"version"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}
type Status struct {
	State     string    `json:"state"`
	Version   string    `json:"version"`
	Sequence  int64     `json:"sequence"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}
type Candidate struct {
	Digest  string
	Length  int64
	Release Release
	Path    string
}

func WriteJSON(path string, v any) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".state-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func Check(ctx context.Context, c Config) (Candidate, error) {
	var out Candidate
	u, e := url.Parse(c.Repository)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && c.AllowHTTPDevelopment)) {
		return out, errors.New("invalid update repository")
	}
	if c.RootFile == "" || c.StateDir == "" {
		return out, errors.New("trusted root and private state directory required")
	}
	root, e := os.ReadFile(c.RootFile)
	if e != nil {
		return out, e
	}
	if e = os.MkdirAll(c.StateDir, 0700); e != nil {
		return out, e
	}
	cfg, e := tufconfig.New(c.Repository, root)
	if e != nil {
		return out, e
	}
	cfg.LocalMetadataDir = filepath.Join(c.StateDir, "metadata")
	cfg.LocalTargetsDir = filepath.Join(c.StateDir, "targets")
	cfg.MaxRootRotations = 16
	client := &http.Client{Transport: contextTransport{ctx: ctx}, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("update redirect rejected") }}
	_ = cfg.SetDefaultFetcherHTTPClient(client)
	_ = cfg.SetDefaultFetcherRetry(time.Second, 1)
	up, e := updater.New(cfg)
	if e != nil {
		return out, e
	}
	if e = up.Refresh(); e != nil {
		return out, errors.New("TUF metadata verification failed")
	}
	if e = ctx.Err(); e != nil {
		return out, e
	}
	target, e := up.GetTargetInfo("nt-agent-" + runtime.GOOS + "-" + runtime.GOARCH)
	if e != nil {
		return out, errors.New("signed platform release unavailable")
	}
	if target.Length <= 0 || target.Length > 128*1024*1024 {
		return out, errors.New("release size rejected")
	}
	if target.Custom == nil || json.Unmarshal(*target.Custom, &out.Release) != nil || out.Release.Sequence <= 0 || out.Release.Version == "" || out.Release.OS != runtime.GOOS || out.Release.Arch != runtime.GOARCH {
		return out, errors.New("invalid signed release manifest")
	}
	var state Status
	if raw, e := os.ReadFile(filepath.Join(c.StateDir, "installed.json")); e == nil {
		if json.Unmarshal(raw, &state) != nil {
			return out, errors.New("invalid installed release state")
		}
	} else if !os.IsNotExist(e) {
		return out, e
	}
	if out.Release.Sequence <= state.Sequence {
		return out, ErrNoUpdate
	}
	var rejected Status
	if raw, e := os.ReadFile(filepath.Join(c.StateDir, "rejected.json")); e == nil {
		if json.Unmarshal(raw, &rejected) != nil {
			return out, errors.New("invalid rejected release state")
		}
		if out.Release.Sequence <= rejected.Sequence {
			return out, errors.New("release previously failed health checks; publish a newer sequence")
		}
	} else if !os.IsNotExist(e) {
		return out, e
	}
	out.Digest = hex.EncodeToString(target.Hashes["sha256"])
	out.Length = target.Length
	out.Path, _, e = up.DownloadTarget(target, filepath.Join(c.StateDir, fmt.Sprintf("candidate-%d", out.Release.Sequence)), "")
	return out, e
}

// Install replaces only a verified candidate on the same filesystem. A failed
// health check restores the previous executable before returning an error.
func Install(ctx context.Context, c Config, candidate Candidate, binary string, health func(context.Context, string) error) error {
	info, e := os.Stat(binary)
	if e != nil || !info.Mode().IsRegular() {
		return errors.New("installed executable missing")
	}
	in, e := os.Open(candidate.Path)
	if e != nil {
		return e
	}
	defer in.Close()
	temp, e := os.CreateTemp(filepath.Dir(binary), ".agent-next-*")
	if e != nil {
		return e
	}
	next := temp.Name()
	defer os.Remove(next)
	hash := sha256.New()
	var copied int64
	if e = temp.Chmod(0700); e == nil {
		copied, e = io.Copy(io.MultiWriter(temp, hash), io.LimitReader(in, 128*1024*1024+1))
		if e == nil && (copied != candidate.Length || hex.EncodeToString(hash.Sum(nil)) != candidate.Digest) {
			e = errors.New("candidate changed after verification")
		}
	}
	if e == nil {
		e = temp.Sync()
	}
	ce := temp.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	backup := fmt.Sprintf("%s.previous.%d", binary, candidate.Release.Sequence)
	if _, e = os.Stat(backup); !os.IsNotExist(e) {
		return errors.New("backup already exists or inaccessible")
	}
	if e = WriteJSON(filepath.Join(c.StateDir, "transaction.json"), map[string]string{"binary": binary, "backup": backup, "phase": "prepared"}); e != nil {
		return e
	}
	if e = os.Rename(binary, backup); e != nil {
		return e
	}
	if e = os.Rename(next, binary); e != nil {
		_ = os.Rename(backup, binary)
		return e
	}
	if e = health(ctx, binary); e != nil {
		restore := os.Rename(backup, binary)
		status := Status{State: "rolled_back", Version: candidate.Release.Version, Sequence: candidate.Release.Sequence, Error: "new process health check failed", CheckedAt: time.Now()}
		_ = WriteJSON(filepath.Join(c.StateDir, "status.json"), status)
		_ = WriteJSON(filepath.Join(c.StateDir, "rejected.json"), status)
		if restore != nil {
			return errors.New("health check failed and executable restore failed")
		}
		_ = os.Remove(filepath.Join(c.StateDir, "transaction.json"))
		return errors.New("new release unhealthy; previous executable restored")
	}
	status := Status{State: "installed", Version: candidate.Release.Version, Sequence: candidate.Release.Sequence, CheckedAt: time.Now()}
	if e = WriteJSON(filepath.Join(c.StateDir, "installed.json"), status); e != nil {
		_ = os.Rename(backup, binary)
		return e
	}
	if e = WriteJSON(filepath.Join(c.StateDir, "status.json"), status); e != nil {
		return e
	}
	return os.Remove(filepath.Join(c.StateDir, "transaction.json"))
}

// Recover an interrupted replacement conservatively: restore the last executable.
func Recover(c Config, binary string) error {
	raw, e := os.ReadFile(filepath.Join(c.StateDir, "transaction.json"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var tx map[string]string
	if json.Unmarshal(raw, &tx) != nil || tx["binary"] != binary || !strings.HasPrefix(tx["backup"], binary+".previous.") || filepath.Dir(tx["backup"]) != filepath.Dir(binary) {
		return errors.New("invalid update transaction")
	}
	if _, e = os.Stat(tx["backup"]); e == nil {
		if e = os.Rename(tx["backup"], binary); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	return os.Remove(filepath.Join(c.StateDir, "transaction.json"))
}

type contextTransport struct{ ctx context.Context }

func (t contextTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return http.DefaultTransport.RoundTrip(r.Clone(t.ctx))
}
