package deploytask

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"puppeteer/backend/internal/agentupdate"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const agentRepositoryNginx = `location ~ "^/agent-updates/([1-9][0-9]{0,3}\.root\.json|targets\.json|snapshot\.json|timestamp\.json|targets/nt-agent-linux-amd64)$" {
 alias /srv/agent-updates/$1;
 default_type application/octet-stream;
 add_header Cache-Control 'no-store';
 add_header X-Content-Type-Options nosniff;
}
location /agent-updates/ { return 404; }
`

type repositoryState struct {
	Trust       string `json:"trust"`
	Sequence    int64  `json:"sequence"`
	Directory   string `json:"directory"`
	Digest      string `json:"digest"`
	RootVersion int64  `json:"rootVersion"`
}

func (d *HostDriver) prepareRepository(ctx context.Context, r Request) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var root any
	if json.Unmarshal([]byte(r.TrustRoot), &root) != nil {
		return "", errors.New("invalid trusted root")
	}
	canonical, _ := json.Marshal(root)
	trustSum := sha256.Sum256(canonical)
	trust := hex.EncodeToString(trustSum[:])
	var prior repositoryState
	if raw, e := os.ReadFile(filepath.Join(d.StateDirectory, "repository-published.json")); e == nil {
		if json.Unmarshal(raw, &prior) != nil || prior.Trust != trust {
			return "", errors.New("更新仓库可信根与既有发布不一致；仅接受原可信根签署的轮换链")
		}
	} else if !os.IsNotExist(e) {
		return "", errors.New("既有仓库发布记录不可读")
	}
	work, e := os.MkdirTemp(d.StateDirectory, "repository-*")
	if e != nil {
		return "", e
	}
	// Keep a root-owned staging parent; only the validated public child is mounted.
	output := filepath.Join(work, "public")
	release, e := agentupdate.FetchRepository(ctx, strings.TrimSuffix(r.Repository, "/"), []byte(r.TrustRoot), output, prior.Sequence, prior.RootVersion)
	if e != nil {
		os.RemoveAll(work)
		return "", errors.New("仓库下载、可信签名、有效期、版本或目标摘要检查失败")
	}
	binary, e := os.ReadFile(filepath.Join(output, "targets/nt-agent-linux-amd64"))
	if e != nil {
		return "", errors.New("签名发布目标不可读取")
	}
	sum := sha256.Sum256(binary)
	digest := hex.EncodeToString(sum[:])
	if release.Sequence == prior.Sequence && prior.Digest != "" && digest != prior.Digest {
		os.RemoveAll(work)
		return "", errors.New("相同发布序号的目标摘要发生变化")
	}
	state := repositoryState{Trust: trust, Sequence: release.Sequence, Directory: output, Digest: digest, RootVersion: release.RootVersion}
	raw, _ := json.Marshal(state)
	if e = atomicPrivate(filepath.Join(work, "verified.json"), raw); e != nil {
		return "", e
	}
	return output, nil
}

func (d *HostDriver) verifyRepository(ctx context.Context, r Request) error {
	if !r.Enabled {
		_, e := d.Executor.run(ctx, 10*time.Second, "verify repository disabled", "exec", "-T", "web", "sh", "-c", "if wget -q --no-check-certificate -T 5 -O /dev/null https://127.0.0.1:8443/agent-updates/timestamp.json; then exit 1; fi")
		return e
	}
	raw, e := d.Executor.Resolve(ctx)
	if e != nil {
		return e
	}
	var model composeModel
	json.Unmarshal(raw, &model)
	var mounts []composeMount
	json.Unmarshal(model.Services["web"]["volumes"], &mounts)
	directory := ""
	for _, m := range mounts {
		if m.Target == "/srv/agent-updates" && m.ReadOnly {
			directory = m.Source
		}
	}
	rel, e := filepath.Rel(d.StateDirectory, directory)
	if e != nil || !strings.HasPrefix(rel, "repository-") || filepath.Base(directory) != "public" {
		return errors.New("更新仓库挂载不属于受管发布")
	}
	proof, e := os.ReadFile(filepath.Join(filepath.Dir(directory), "verified.json"))
	if e != nil {
		return errors.New("签名发布证据缺失")
	}
	var state repositoryState
	if json.Unmarshal(proof, &state) != nil || state.Directory != directory {
		return errors.New("签名发布证据不匹配")
	}
	// Download the public target through Nginx, then compare its SHA-256 to the
	// verified signed manifest; this catches a wrong mount or stale release.
	observed, e := d.Executor.run(ctx, 30*time.Second, "verify signed repository download", "exec", "-T", "web", "sh", "-c", "set -eu; f=$(mktemp /tmp/agent-verify-XXXXXX); trap 'rm -f \"$f\"' EXIT; wget -q --no-check-certificate -T 10 -O \"$f\" https://127.0.0.1:8443/agent-updates/targets/nt-agent-linux-amd64; sha256sum \"$f\"")
	if e != nil || !strings.HasPrefix(string(observed), state.Digest+" ") {
		return errors.New("已发布 Agent 文件下载或签名摘要复核失败")
	}
	for _, name := range []string{"1.root.json", "targets.json", "snapshot.json", "timestamp.json"} {
		if _, e = d.Executor.run(ctx, 10*time.Second, "verify repository metadata", "exec", "-T", "web", "wget", "-q", "--no-check-certificate", "-T", "5", "-O", "/dev/null", fmt.Sprintf("https://127.0.0.1:8443/agent-updates/%s", name)); e != nil {
			return e
		}
	}
	return atomicPrivate(filepath.Join(d.StateDirectory, "repository-published.json"), proof)
}

func (d *HostDriver) cleanupRepositoryPlan(plan ComposePlan, r Request) {
	if r.Protocol != "agent-repository" || !r.Enabled {
		return
	}
	var model composeModel
	json.Unmarshal(plan.Document, &model)
	var mounts []composeMount
	json.Unmarshal(model.Services["web"]["volumes"], &mounts)
	for _, m := range mounts {
		if m.Target == "/srv/agent-updates" && filepath.Base(m.Source) == "public" {
			parent := filepath.Dir(m.Source)
			if filepath.Dir(parent) == d.StateDirectory && strings.HasPrefix(filepath.Base(parent), "repository-") {
				os.RemoveAll(parent)
			}
		}
	}
}
