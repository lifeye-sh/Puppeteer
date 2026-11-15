package agentupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/theupdateframework/go-tuf/v2/metadata"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// FetchRepository stages only public allowlisted files. Trust must be supplied
// independently; a self-signed root fetched from the same URL is not a trust anchor.
// The caller promotes the new output only after this function succeeds.
type VerifiedRelease struct {
	Release
	RootVersion int64
}

func FetchRepository(ctx context.Context, endpoint string, trust []byte, output string, minimum int64, minimumRoot ...int64) (VerifiedRelease, error) {
	var release VerifiedRelease
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return release, errors.New("invalid repository URL")
	}
	root, e := metadata.Root().FromBytes(trust)
	if e != nil || root.Signed.Version != 1 || root.VerifyDelegate("root", root) != nil {
		return release, errors.New("trusted version-one root required")
	}
	parent := filepath.Dir(output)
	stage, e := os.MkdirTemp(parent, ".repo-download-*")
	if e != nil {
		return release, e
	}
	defer os.RemoveAll(stage)
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	fetch := func(name string, limit int64) ([]byte, int, error) {
		target := *u
		target.Path = target.Path + "/" + name
		req, e := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
		if e != nil {
			return nil, 0, e
		}
		res, e := client.Do(req)
		if e != nil {
			return nil, 0, errors.New("repository HTTPS request failed")
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return nil, res.StatusCode, errors.New("repository file unavailable")
		}
		raw, e := io.ReadAll(io.LimitReader(res.Body, limit+1))
		if e != nil || len(raw) == 0 || int64(len(raw)) > limit {
			return nil, res.StatusCode, errors.New("repository file invalid or oversized")
		}
		return raw, 200, nil
	}
	previous := root
	for n := 1; n <= 17; n++ {
		name := fmt.Sprintf("%d.root.json", n)
		raw, status, e := fetch(name, 4<<20)
		if n > 1 && status == 404 {
			break
		}
		if e != nil {
			return release, e
		}
		if n == 17 {
			return release, errors.New("root chain exceeds managed rotation limit")
		}
		current, e := metadata.Root().FromBytes(raw)
		if e != nil || current.Signed.Version != int64(n) || current.VerifyDelegate("root", current) != nil || previous.VerifyDelegate("root", current) != nil {
			return release, errors.New("root chain signature invalid")
		}
		if n == 1 {
			canonical, _ := root.ToBytes(false)
			actual, _ := current.ToBytes(false)
			if !bytes.Equal(canonical, actual) {
				return release, errors.New("repository root does not match independent trust")
			}
		}
		previous = current
		if e = os.WriteFile(filepath.Join(stage, name), raw, 0600); e != nil {
			return release, e
		}
	}
	if e = os.Mkdir(filepath.Join(stage, "targets"), 0700); e != nil {
		return release, e
	}
	release.RootVersion = previous.Signed.Version
	if len(minimumRoot) > 0 && release.RootVersion < minimumRoot[0] {
		return release, errors.New("root version rollback rejected")
	}
	for _, name := range []string{"targets.json", "snapshot.json", "timestamp.json", "targets/nt-agent-linux-amd64"} {
		limit := int64(4 << 20)
		if name == "targets/nt-agent-linux-amd64" {
			limit = 128 << 20
		}
		raw, _, e := fetch(name, limit)
		if e != nil {
			return release, e
		}
		if e = os.WriteFile(filepath.Join(stage, filepath.FromSlash(name)), raw, 0600); e != nil {
			return release, e
		}
	}
	targets, e := metadata.Targets().FromFile(filepath.Join(stage, "targets.json"))
	if e != nil {
		return release, e
	}
	target := targets.Signed.Targets["nt-agent-linux-amd64"]
	if target == nil || target.Custom == nil || json.Unmarshal(*target.Custom, &release.Release) != nil || release.Sequence < minimum {
		return release, errors.New("release sequence rollback rejected")
	}
	if e = ExportRepository(stage, output); e != nil {
		return release, errors.New("signed repository export verification failed")
	}
	return release, nil
}
