//go:build linux

package hostupgrade

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"puppeteer/backend/internal/platform/buildmeta"
	"puppeteer/backend/internal/platform/deploytask"
	"puppeteer/backend/internal/platform/systemupdate"
)

type Request struct {
	ID           string          `json:"id"`
	Version      string          `json:"version"`
	Actor        string          `json:"actor"`
	Envelope     json.RawMessage `json:"envelope"`
	LayoutDigest string          `json:"layoutDigest"`
	Generation   uint64          `json:"generation"`
}

type Progress struct {
	ID            string                      `json:"id"`
	Source        string                      `json:"source"`
	Target        string                      `json:"target"`
	Stage         string                      `json:"stage"`
	Reason        string                      `json:"reason,omitempty"`
	Started       time.Time                   `json:"started"`
	Updated       time.Time                   `json:"updated"`
	Events        []systemupdate.UpgradeEvent `json:"events"`
	Terminal      bool                        `json:"terminal"`
	ProgressToken string                      `json:"progressToken,omitempty"`
}

func progress(j systemupdate.UpgradeJournal) Progress {
	return Progress{ID: j.Plan.ID, Source: j.Plan.Source, Target: j.Plan.Target, Stage: j.Stage, Reason: j.Reason, Started: j.Started, Updated: j.Updated, Events: j.Events, Terminal: j.Terminal() || j.Stage == "recovery_required"}
}

type Controller struct {
	Engine     *deploytask.Engine
	Executor   deploytask.ComposeExecutor
	ConfigPath string
	mu         sync.Mutex
	policyMu   sync.Mutex
}

func (c *Controller) directory() string {
	return filepath.Join(c.Executor.Directory, "config", "upgrade-state")
}
func (c *Controller) unit() string { return "puppeteer-upgrade-" + c.Executor.Project + ".service" }

func (c *Controller) Available() (bool, string) {
	pid, _ := os.ReadFile("/proc/1/comm")
	if strings.TrimSpace(string(pid)) != "systemd" {
		return false, "需要宿主机 systemd，容器化执行器暂不支持自升级"
	}
	if _, e := exec.LookPath("python3"); e != nil {
		return false, "宿主机缺少 Python 3"
	}
	if _, e := exec.LookPath("systemd-run"); e != nil {
		return false, "宿主机缺少独立任务启动能力"
	}
	if len(c.Executor.Files) != 1 || c.Executor.Files[0] != filepath.Join(c.Executor.Directory, "compose-managed.json") {
		return false, "需要标准受管 Compose 布局"
	}
	if c.ConfigPath != filepath.Join(c.Executor.Directory, "config", "deployd.json") {
		return false, "执行器配置路径不是标准受管布局"
	}
	progressConfig, e := os.ReadFile(filepath.Join(c.Executor.Directory, "config", "deployment-progress.conf"))
	if e != nil || !strings.Contains(string(progressConfig), "/upgrade-progress/") {
		return false, "请先通过受管升级脚本接入独立升级进度入口"
	}
	return true, "独立升级进程可用；实际停机前仍会完成镜像、空间、排空与备份检查"
}

func (c *Controller) plan(r Request) (systemupdate.UpgradePlan, error) {
	raw, e := os.ReadFile(c.Executor.Files[0])
	if e != nil {
		return systemupdate.UpgradePlan{}, errors.New("无法读取受管配置")
	}
	hash := sha256.Sum256(raw)
	layout := hex.EncodeToString(hash[:])
	if r.LayoutDigest != "" && r.LayoutDigest != layout {
		return systemupdate.UpgradePlan{}, errors.New("部署配置已变化，请重新预检")
	}
	return systemupdate.UpgradePlan{ID: r.ID, Actor: r.Actor, Source: buildmeta.Release, Target: r.Version, LayoutDigest: layout, Envelope: r.Envelope}, nil
}

func (c *Controller) check(ctx context.Context, r Request) (systemupdate.UpgradePlan, error) {
	if ok, reason := c.Available(); !ok {
		return systemupdate.UpgradePlan{}, errors.New(reason)
	}
	p, e := c.plan(r)
	if e != nil {
		return p, e
	}
	if _, e = systemupdate.ResolveTarget(p, systemupdate.TrustedKeys()); e != nil {
		return p, e
	}
	args := []string{"compose", "--project-directory", c.Executor.Directory, "--project-name", c.Executor.Project, "-f", c.Executor.Files[0], "exec", "-T", "server", "/app/nt", "upgrade-control", "check", r.Actor}
	cmd := exec.CommandContext(ctx, c.Executor.Docker, args...)
	cmd.Stderr = io.Discard
	out := &cappedBuffer{limit: 65536}
	cmd.Stdout = out
	cmd.WaitDelay = time.Second
	var response struct {
		Passed  bool   `json:"passed"`
		Version string `json:"version"`
		Error   string `json:"error"`
	}
	if cmd.Run() != nil || json.Unmarshal(out.Bytes(), &response) != nil || !response.Passed {
		return p, errors.New("应用存在维护或执行任务、版本不支持排空，或管理员权限已变化")
	}
	if response.Version != p.Source {
		return p, errors.New("后端与执行器版本不一致，不能自动升级")
	}
	return p, nil
}

func (c *Controller) Status() (*Progress, error) {
	tx, e := systemupdate.OpenTransaction(c.directory(), nil)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, errors.New("升级记录不可读取，暂时禁止其他部署")
	}
	j := tx.View()
	p := progress(j)
	if time.Since(j.Started) < 4*time.Hour {
		raw, _ := os.ReadFile(filepath.Join(c.directory(), j.Plan.ID, "progress-token"))
		p.ProgressToken = string(raw)
	}
	return &p, nil
}

func privateBytes(path string, raw []byte, mode os.FileMode) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".upgrade-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}

func (c *Controller) prepareRunner(p systemupdate.UpgradePlan) error {
	dir := filepath.Join(c.directory(), p.ID)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	self, e := os.Executable()
	if e != nil {
		return e
	}
	raw, e := os.ReadFile(self)
	if e != nil {
		return e
	}
	if e = privateBytes(filepath.Join(dir, "runner"), raw, 0700); e != nil {
		return e
	}
	hash := sha256.Sum256(raw)
	if e = privateBytes(filepath.Join(dir, "runner.sha256"), []byte(hex.EncodeToString(hash[:])), 0600); e != nil {
		return e
	}
	ticket := make([]byte, 32)
	if _, e = rand.Read(ticket); e != nil {
		return e
	}
	return privateBytes(filepath.Join(dir, "progress-token"), []byte(hex.EncodeToString(ticket)), 0600)
}

// Resume is also called after daemon or host restart. The copied binary and
// persistent intent survive replacement of both the application and deployd.
func (c *Controller) Resume(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, e := systemupdate.OpenTransaction(c.directory(), nil)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	j := tx.View()
	if j.Terminal() {
		return nil
	}
	probe := exec.CommandContext(ctx, "systemctl", "show", c.unit(), "--property=ActiveState", "--value")
	raw, _ := probe.Output()
	state := strings.TrimSpace(string(raw))
	if state == "active" || state == "activating" || state == "deactivating" {
		return nil
	}
	dir := filepath.Join(c.directory(), j.Plan.ID)
	binary := filepath.Join(dir, "runner")
	info, e := os.Lstat(binary)
	if e != nil {
		return errors.New("独立升级进程文件缺失")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return errors.New("独立升级进程文件权限异常")
	}
	raw, e = os.ReadFile(binary)
	if e != nil {
		return e
	}
	hash := sha256.Sum256(raw)
	expected, e := os.ReadFile(filepath.Join(dir, "runner.sha256"))
	if e != nil || string(expected) != hex.EncodeToString(hash[:]) {
		return errors.New("独立升级进程摘要不匹配")
	}
	cmd := exec.CommandContext(ctx, "systemd-run", "--collect", "--unit="+c.unit(), "--property=Restart=on-failure", "--property=RestartSec=3", "--property=TimeoutStopSec=900", "--property=UMask=0077", binary, "upgrade-run", "--config", c.ConfigPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if cmd.Run() != nil {
		return errors.New("独立升级进程尚未启动；执行器会重试，实例仍保持部署互斥")
	}
	return nil
}

func (c *Controller) Run(ctx context.Context) {
	timer := time.NewTicker(10 * time.Second)
	defer timer.Stop()
	for {
		call, done := context.WithTimeout(ctx, 8*time.Second)
		_ = c.Resume(call)
		done()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func (c *Controller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/updates/automation" {
		c.AutomationHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	respond := func(code int, value any) { w.WriteHeader(code); _ = json.NewEncoder(w).Encode(value) }
	if r.Method == "GET" && r.URL.Path == "/v1/updates/history" {
		items, e := c.History()
		if e != nil {
			respond(503, map[string]string{"error": "升级历史不可读取"})
			return
		}
		respond(200, map[string]any{"items": items})
		return
	}
	if r.Method == "GET" && r.URL.Path == "/v1/updates/task" {
		p, e := c.Status()
		if e != nil {
			respond(503, map[string]string{"error": e.Error()})
			return
		}
		respond(200, map[string]any{"task": p})
		return
	}
	if r.Method != "POST" || (r.URL.Path != "/v1/updates/preflight" && r.URL.Path != "/v1/updates/tasks") {
		respond(404, map[string]string{"error": "unsupported update operation"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2*systemupdate.MaxIndexBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input Request
	var extra any
	if decoder.Decode(&input) != nil || decoder.Decode(&extra) != io.EOF {
		respond(400, map[string]string{"error": "invalid upgrade request"})
		return
	}
	if r.URL.Path == "/v1/updates/tasks" {
		if old, e := systemupdate.OpenTransaction(c.directory(), nil); e == nil && old.View().Plan.ID == input.ID {
			p := old.View().Plan
			if p.Actor != input.Actor || p.Target != input.Version || p.LayoutDigest != input.LayoutDigest {
				respond(409, map[string]string{"error": "升级请求标识已用于其他计划"})
				return
			}
			view, _ := c.Status()
			respond(202, map[string]any{"task": view})
			return
		}
	}
	var plan systemupdate.UpgradePlan
	err := c.Engine.Maintain(r.Context(), func(ctx context.Context) error {
		var e error
		plan, e = c.check(ctx, input)
		if e != nil {
			return e
		}
		generation, _ := c.Engine.View()
		if r.URL.Path == "/v1/updates/preflight" {
			input.Generation = generation
			return nil
		}
		if input.LayoutDigest == "" || input.Generation != generation {
			return errors.New("部署状态已变化，请重新预检")
		}
		unlock, e := systemupdate.LockTransaction(c.Executor.Directory)
		if e != nil {
			return e
		}
		defer unlock()
		if _, e = os.Lstat(filepath.Join(c.directory(), plan.ID+".json")); !os.IsNotExist(e) {
			return errors.New("该升级请求已归档，请重新预检生成新请求")
		}
		if e = c.prepareRunner(plan); e != nil {
			return errors.New("无法保存独立升级进程，请检查宿主机空间和权限")
		}
		_, e = systemupdate.CreateTransaction(c.directory(), plan, systemupdate.TrustedKeys())
		return e
	})
	if err != nil {
		status := 422
		if errors.Is(err, deploytask.ErrBusy) || errors.Is(err, systemupdate.ErrUpgradeBusy) {
			status = 409
		}
		respond(status, map[string]string{"error": err.Error()})
		return
	}
	if r.URL.Path == "/v1/updates/preflight" {
		respond(200, map[string]any{"passed": true, "layoutDigest": plan.LayoutDigest, "generation": input.Generation, "source": plan.Source})
		return
	}
	ctx, done := context.WithTimeout(context.Background(), 8*time.Second)
	launchErr := c.Resume(ctx)
	done()
	view, e := c.Status()
	if e != nil {
		respond(503, map[string]string{"error": e.Error()})
		return
	}
	message := ""
	if launchErr != nil {
		message = launchErr.Error()
	}
	respond(202, map[string]any{"task": view, "notice": message})
}
