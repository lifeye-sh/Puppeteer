package deploytask

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// HostDriver operates only on an enrolled standalone Compose file. The daemon
// must hold an exclusive deployment lock. All paths are installer-owned.
type HostDriver struct {
	Executor       ComposeExecutor
	StateDirectory string
	// Check validates application policy, certificates and network capabilities.
	// No deployment is allowed before this integration is installed.
	Check         func(context.Context, Request) error
	VerifyRuntime func(context.Context, Request) error
	ApplyWebsite  func(context.Context, Request) error
	Progress      func(string) error
	WALGuard      func(context.Context) error
}
type hostState struct {
	Owned         map[string]*Mapping `json:"owned"`
	Previous      json.RawMessage     `json:"previous,omitempty"`
	PreviousOwned map[string]*Mapping `json:"previousOwned"`
	Request       *Request            `json:"request,omitempty"`
}

func atomicPrivate(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".deploy-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (d *HostDriver) load() (hostState, error) {
	var state hostState
	raw, err := os.ReadFile(filepath.Join(d.StateDirectory, "deployment.json"))
	if err != nil {
		return state, errors.New("deployment enrollment missing or unreadable")
	}
	if json.Unmarshal(raw, &state) != nil || state.Owned == nil {
		return state, errors.New("invalid deployment enrollment")
	}
	return state, nil
}
func (d *HostDriver) save(s hostState) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return atomicPrivate(filepath.Join(d.StateDirectory, "deployment.json"), raw)
}
func (d *HostDriver) Preflight(ctx context.Context, r Request) error {
	if specialized(r.Protocol) {
		return d.specializedPreflight(ctx, r)
	}
	if len(d.Executor.Files) != 1 {
		return errors.New("standalone Compose enrollment required")
	}
	if d.Check == nil {
		return errors.New("application and network preflight unavailable")
	}
	if err := d.Check(ctx, r); err != nil {
		return err
	}
	state, err := d.load()
	if err != nil {
		return err
	}
	raw, err := d.Executor.Resolve(ctx)
	if err != nil {
		return err
	}
	plan, err := d.plan(ctx, raw, r, state.Owned[r.Protocol])
	if err != nil {
		return err
	}
	if err = d.validatePlan(ctx, plan, r); err != nil {
		return err
	}
	if r.Protocol == "mp4" && r.Enabled {
		if _, err = d.Executor.run(ctx, 5*time.Second, "check MP4 cache permissions", "exec", "-T", "server", "sh", "-c", "if test -e /var/lib/puppeteer/mp4; then test -d /var/lib/puppeteer/mp4 && test -w /var/lib/puppeteer/mp4; else test -w /var/lib/puppeteer; fi"); err != nil {
			return errors.New("MP4 缓存目录不可写")
		}
	}
	if r.Protocol == "rdp-drive" && r.Enabled {
		if err = d.Executor.verifyGraphics(ctx, true); err != nil {
			return errors.New("共享盘依赖运行健康的图形组件和应用入口")
		}
		if _, err = d.Executor.run(ctx, 5*time.Second, "check storage config volume", "exec", "-T", "server", "sh", "-c", "test -d /var/lib/puppeteer/storage && test -w /var/lib/puppeteer/storage"); err != nil {
			return errors.New("应用的 storageconfig 卷不可写或归属不正确")
		}
	}
	if r.Protocol == "graphics" && r.Enabled {
		if err = checkGraphicsDevices(plan.Document); err != nil {
			return err
		}
	}
	if r.Enabled && r.Protocol != "management" && r.Protocol != "graphics" && r.Protocol != "rdp-drive" && r.Protocol != "mp4" {
		owned := state.Owned[r.Protocol]
		if owned != nil && owned.Bind == r.Bind && owned.Published == r.Port {
			previous := r
			if err = d.Executor.Verify(ctx, previous); err != nil {
				return errors.New("existing owned mapping cannot be verified; reconcile deployment first")
			}
		} else {
			if err = d.Executor.CheckPublishedPort(ctx, r.Bind, r.Port); err != nil {
				return err
			}
			if err = probeHostPort(ctx, r.Bind, r.Port); err != nil {
				return err
			}
		}
	}
	return nil
}

// Compose interpolates dollar signs even in JSON strings. Escape resolved values
// before persisting so secrets and literal dollar signs survive the next parse.
func literalCompose(raw []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	var escape func(any) any
	escape = func(v any) any {
		switch x := v.(type) {
		case string:
			return strings.ReplaceAll(x, "$", "$$")
		case []any:
			for i := range x {
				x[i] = escape(x[i])
			}
		case map[string]any:
			for k := range x {
				x[k] = escape(x[k])
			}
		}
		return v
	}
	return json.Marshal(escape(value))
}
func (d *HostDriver) Apply(ctx context.Context, r Request) error {
	if specialized(r.Protocol) {
		return d.specializedApply(ctx, r)
	}
	if err := d.Preflight(ctx, r); err != nil {
		return err
	}
	state, err := d.load()
	if err != nil {
		return err
	}
	raw, err := d.Executor.Resolve(ctx)
	if err != nil {
		return err
	}
	plan, err := d.plan(ctx, raw, r, state.Owned[r.Protocol])
	if err != nil {
		return err
	}
	prior, err := os.ReadFile(d.Executor.Files[0])
	if err != nil {
		return err
	}
	state.Previous = prior
	state.PreviousOwned = state.Owned
	state.Request = &r
	// Journal precedes the config replacement; it contains the only recovery copy.
	if err = d.save(state); err != nil {
		return err
	}
	if r.SiteID != "" && !r.SiteEnabled {
		if d.ApplyWebsite == nil {
			return errors.New("website operation unavailable")
		}
		if err = d.ApplyWebsite(ctx, r); err != nil {
			return err
		}
	}
	if err = d.prepareAuxiliary(plan); err != nil {
		return err
	}
	document, err := literalCompose(plan.Document)
	if err != nil {
		return err
	}
	if err = atomicPrivate(d.Executor.Files[0], document); err != nil {
		return err
	}
	next := map[string]*Mapping{}
	for key, mapping := range state.Owned {
		next[key] = mapping
	}
	if plan.Next == nil {
		delete(next, r.Protocol)
	} else {
		next[r.Protocol] = plan.Next
	}
	state.Owned = next
	if err = d.save(state); err != nil {
		return err
	}
	if r.Protocol == "mp4" {
		return d.Executor.applyMP4(ctx, r.Enabled)
	}
	if r.Protocol == "rdp-drive" {
		return d.applyDrive(ctx, plan.Document, r)
	}
	if r.Protocol == "graphics" {
		return d.Executor.applyGraphics(ctx, r.Enabled)
	}
	return d.Executor.Apply(ctx)
}
func (d *HostDriver) Verify(ctx context.Context, r Request) error {
	if specialized(r.Protocol) {
		return d.specializedVerify(ctx, r)
	}
	if err := d.Executor.Verify(ctx, r); err != nil {
		return err
	}
	if d.Check == nil {
		return errors.New("application verification unavailable")
	}
	if err := d.Check(ctx, r); err != nil {
		return err
	}
	if d.VerifyRuntime != nil {
		if err := d.VerifyRuntime(ctx, r); err != nil {
			return err
		}
	}
	if r.SiteID != "" && r.SiteEnabled {
		if d.ApplyWebsite == nil {
			return errors.New("website operation unavailable")
		}
		return d.ApplyWebsite(ctx, r)
	}
	return nil
}
func (d *HostDriver) Rollback(ctx context.Context, r Request) error {
	if specialized(r.Protocol) {
		return d.specializedRecover(ctx, r, false)
	}
	if r.IsDisable() {
		return errors.New("disable operations cannot be rolled back")
	}
	state, err := d.load()
	if err != nil {
		return err
	}
	if state.Request == nil || *state.Request != r || len(state.Previous) == 0 {
		return errors.New("matching recovery configuration missing")
	}
	if err = atomicPrivate(d.Executor.Files[0], state.Previous); err != nil {
		return err
	}
	state.Owned = state.PreviousOwned
	if err = d.save(state); err != nil {
		return err
	}
	if r.Protocol == "mp4" {
		return d.Executor.applyMP4(ctx, mp4Enabled(state.Previous))
	}
	if r.Protocol == "rdp-drive" {
		previous := r
		previous.Enabled = driveDesired(state.Previous)
		return d.applyDrive(ctx, state.Previous, previous)
	}
	if r.Protocol == "graphics" {
		return d.Executor.applyGraphics(ctx, graphicsEnabled(state.Previous))
	}
	return d.Executor.Apply(ctx)
}
func (d *HostDriver) Fence(ctx context.Context, r Request) error {
	if specialized(r.Protocol) {
		return d.specializedRecover(ctx, r, true)
	}
	if !r.IsDisable() {
		return errors.New("cannot fence an enable request")
	}
	if r.Protocol == "mp4" {
		if err := d.Executor.stopMP4(ctx); err != nil {
			return err
		}
	}
	if r.Protocol == "graphics" || r.Protocol == "rdp-drive" {
		if err := d.Executor.stopGraphics(ctx); err != nil {
			return err
		}
	}
	// Reconcile from the recorded recovery snapshot rather than guessing ownership
	// after a crash between configuration replacement and journal update.
	state, err := d.load()
	if err != nil {
		return err
	}
	if state.Request == nil || *state.Request != r || len(state.Previous) == 0 {
		return errors.New("matching disable recovery configuration missing")
	}
	if r.SiteID != "" && !r.SiteEnabled {
		if d.ApplyWebsite == nil {
			return errors.New("website operation unavailable")
		}
		if err = d.ApplyWebsite(ctx, r); err != nil {
			return err
		}
	}
	previous := d.Executor
	// Resolve the previous configuration privately without activating it.
	path := filepath.Join(d.StateDirectory, "fence-source.json")
	if err = atomicPrivate(path, state.Previous); err != nil {
		return err
	}
	defer os.Remove(path)
	previous.Files = []string{path}
	raw, err := previous.Resolve(ctx)
	if err != nil {
		return err
	}
	plan, err := d.plan(ctx, raw, r, state.PreviousOwned[r.Protocol])
	if err != nil {
		return err
	}
	raw, err = literalCompose(plan.Document)
	if err != nil {
		return err
	}
	if err = atomicPrivate(d.Executor.Files[0], raw); err != nil {
		return err
	}
	if plan.Next == nil {
		delete(state.Owned, r.Protocol)
	} else {
		state.Owned[r.Protocol] = plan.Next
	}
	if err = d.save(state); err != nil {
		return err
	}
	if r.Protocol == "mp4" {
		err = d.Executor.applyMP4(ctx, false)
	} else if r.Protocol == "rdp-drive" {
		err = d.applyDrive(ctx, plan.Document, r)
	} else if r.Protocol == "graphics" {
		err = d.Executor.applyGraphics(ctx, false)
	} else {
		err = d.Executor.Apply(ctx)
	}
	if err != nil {
		return err
	}
	if err = d.Executor.Verify(ctx, r); err != nil {
		return err
	}
	if d.VerifyRuntime != nil {
		return d.VerifyRuntime(ctx, r)
	}
	return nil
}

// A successful bind only proves local availability at this instant. Docker and
// post-apply verification remain authoritative if another process races us.
func probeHostPort(ctx context.Context, bind string, port int) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", net.JoinHostPort(bind, strconv.Itoa(port)))
	if err != nil {
		return errors.New("host address unavailable or TCP port already occupied")
	}
	return listener.Close()
}

func (d *HostDriver) Mappings() (map[string]*Mapping, error) {
	state, err := d.load()
	if err != nil {
		return nil, err
	}
	return state.Owned, nil
}

func (d *HostDriver) plan(ctx context.Context, raw []byte, r Request, owned *Mapping) (ComposePlan, error) {
	if r.Protocol == "mp4" {
		return planMP4(raw, r)
	}
	if r.Protocol == "rdp-drive" {
		return d.drivePlan(ctx, raw, r)
	}
	if r.Protocol == "graphics" {
		return planGraphics(raw, r)
	}
	if r.Protocol != "management" {
		return PlanCompose(raw, r, owned)
	}
	source, err := nginxSource(raw)
	if err != nil {
		return ComposePlan{}, err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return ComposePlan{}, errors.New("Nginx configuration unreadable")
	}
	root, err := filepath.EvalSymlinks(d.Executor.Directory)
	if err != nil {
		return ComposePlan{}, err
	}
	rel, err := filepath.Rel(root, source)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ComposePlan{}, errors.New("Nginx configuration is outside enrolled installation")
	}
	info, err := os.Stat(source)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return ComposePlan{}, errors.New("unsupported Nginx configuration file")
	}
	nginx, err := os.ReadFile(source)
	if err != nil {
		return ComposePlan{}, err
	}
	return planManagement(raw, nginx, r, d.StateDirectory)
}
func (d *HostDriver) prepareAuxiliary(plan ComposePlan) error {
	if plan.AuxiliaryPath == "" {
		return nil
	}
	if err := atomicPrivate(plan.AuxiliaryPath, plan.AuxiliaryContent); err != nil {
		return err
	}
	return os.Chmod(plan.AuxiliaryPath, 0644)
}
func (d *HostDriver) validatePlan(ctx context.Context, plan ComposePlan, r Request) error {
	if err := d.prepareAuxiliary(plan); err != nil {
		return err
	}
	document, err := literalCompose(plan.Document)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(d.StateDirectory, ".preflight-*.json")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(document); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	candidate := d.Executor
	candidate.Files = []string{name}
	if err = candidate.Validate(ctx); err != nil {
		return err
	}
	if r.Protocol == "management" {
		return candidate.ValidateNginx(ctx)
	}
	return nil
}
