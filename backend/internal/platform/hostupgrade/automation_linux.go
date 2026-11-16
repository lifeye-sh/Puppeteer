//go:build linux

package hostupgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"puppeteer/backend/internal/platform/buildmeta"
	"puppeteer/backend/internal/platform/systemupdate"
)

type AutomationState struct {
	Notices       []AutomationNotice            `json:"notices,omitempty"`
	LastCleanup   time.Time                     `json:"lastCleanup,omitempty"`
	CleanupReason string                        `json:"cleanupReason,omitempty"`
	Policy        systemupdate.AutomationPolicy `json:"policy"`
	Actor         string                        `json:"actor,omitempty"`
	LastCheck     time.Time                     `json:"lastCheck,omitempty"`
	LastSlot      string                        `json:"lastSlot,omitempty"`
	LastTask      string                        `json:"lastTask,omitempty"`
	LastStage     string                        `json:"lastStage,omitempty"`
	Reason        string                        `json:"reason,omitempty"`
	Suspended     bool                          `json:"suspended"`
}

type AutomationNotice struct {
	Task   string `json:"task"`
	Stage  string `json:"stage"`
	Actor  string `json:"actor"`
	Target string `json:"target"`
	Reason string `json:"reason"`
}

func (c *Controller) reconcileAutomation(state *AutomationState) bool {
	j, e := systemupdate.ReadUpgradeTask(c.directory(), state.LastTask)
	if e != nil {
		return false
	}
	terminal := j.Terminal() || j.Stage == "recovery_required"
	recorded := false
	for _, n := range state.Notices {
		if n.Task == j.Plan.ID && n.Stage == j.Stage {
			recorded = true
			break
		}
	}
	if state.LastStage == j.Stage && (!terminal || recorded) {
		return false
	}
	state.LastStage, state.Reason = j.Stage, j.Reason
	if j.Stage == "failed" || j.Stage == "rolled_back" || j.Stage == "recovery_required" {
		state.Suspended = true
		state.Reason = "自动执行已暂停：" + j.Reason
	}
	if terminal && !recorded {
		state.Notices = append(state.Notices, AutomationNotice{Task: j.Plan.ID, Stage: j.Stage, Actor: state.Actor, Target: j.Plan.Target, Reason: j.Reason})
		if len(state.Notices) > 30 {
			state.Notices = state.Notices[len(state.Notices)-30:]
		}
	}
	return true
}

func (c *Controller) automationPath() string { return filepath.Join(c.directory(), "automation.json") }
func (c *Controller) automationState() (AutomationState, error) {
	state := AutomationState{Policy: systemupdate.DefaultAutomation()}
	raw, e := os.ReadFile(c.automationPath())
	if os.IsNotExist(e) {
		return state, nil
	}
	if e != nil {
		return state, e
	}
	if len(raw) > 65536 || json.Unmarshal(raw, &state) != nil || state.Policy.Revision == 0 || len(state.Notices) > 30 {
		return state, errors.New("自动升级授权记录损坏，禁止自动执行")
	}
	if state.Policy.Validate(state.Policy.ExpiresAt.Add(-time.Minute)) != nil || (state.Policy.Mode != "off" && state.Actor == "") {
		return state, errors.New("自动升级授权内容无效，禁止自动执行")
	}
	return state, nil
}
func (c *Controller) saveAutomation(s AutomationState) error {
	if e := os.MkdirAll(c.directory(), 0700); e != nil {
		return e
	}
	raw, e := json.Marshal(s)
	if e != nil {
		return e
	}
	return privateBytes(c.automationPath(), raw, 0600)
}

func (c *Controller) AutomationHTTP(w http.ResponseWriter, r *http.Request) {
	c.policyMu.Lock()
	defer c.policyMu.Unlock()
	respond := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	state, e := c.automationState()
	if e != nil {
		respond(503, map[string]string{"error": "自动升级授权记录不可读取"})
		return
	}
	if r.Method == "GET" {
		if c.reconcileAutomation(&state) {
			if c.saveAutomation(state) != nil {
				respond(503, map[string]string{"error": "自动升级结果无法持久化"})
				return
			}
		}
		respond(200, state)
		return
	}
	if r.Method != "POST" {
		respond(405, map[string]string{"error": "unsupported automation operation"})
		return
	}
	var input struct {
		Policy        systemupdate.AutomationPolicy `json:"policy"`
		Actor         string                        `json:"actor"`
		ConfirmedStop bool                          `json:"confirmedStop"`
		Envelope      json.RawMessage               `json:"envelope"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2*systemupdate.MaxIndexBytes)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&input) != nil || d.Decode(&extra) != io.EOF {
		respond(400, map[string]string{"error": "invalid automation policy"})
		return
	}
	if e = input.Policy.Validate(time.Now()); e != nil {
		respond(422, map[string]string{"error": e.Error()})
		return
	}
	if input.Policy.Mode == "window" && !input.ConfirmedStop {
		respond(422, map[string]string{"error": "请确认维护窗口内允许停止会话"})
		return
	}
	if input.Policy.Revision != state.Policy.Revision {
		respond(409, map[string]string{"error": "自动升级授权已变化，请刷新"})
		return
	}
	e = c.Engine.Maintain(r.Context(), func(ctx context.Context) error {
		unlock, e := systemupdate.LockTransaction(c.Executor.Directory)
		if e != nil {
			return e
		}
		defer unlock()
		if input.Policy.Mode != "off" {
			p, e := c.check(ctx, Request{ID: "00000000000000000000000000000000", Version: input.Policy.MaxVersion, Actor: input.Actor, Envelope: input.Envelope})
			if e != nil {
				return e
			}
			release, e := systemupdate.ResolveTarget(p, systemupdate.TrustedKeys())
			if e != nil {
				return e
			}
			if release.Channel != input.Policy.Channel {
				return errors.New("授权上限不属于所选发布渠道")
			}
		}
		input.Policy.Revision++
		c.reconcileAutomation(&state)
		state = AutomationState{Policy: input.Policy, Actor: input.Actor, Notices: state.Notices, Reason: "授权已保存；任务开始后不能通过关闭策略中断升级或恢复"}
		return c.saveAutomation(state)
	})
	if e != nil {
		respond(409, map[string]string{"error": e.Error()})
		return
	}
	respond(200, state)
}

// Tick is host-owned. Application database restoration cannot re-enable an old
// authorization, and a browser never supplies a target to this scheduler.
func (c *Controller) automationTick(ctx context.Context, now time.Time) {
	c.policyMu.Lock()
	defer c.policyMu.Unlock()
	state, e := c.automationState()
	if e != nil {
		return
	}
	if c.reconcileAutomation(&state) && c.saveAutomation(state) != nil {
		return
	}
	if state.Suspended || state.Policy.Mode == "off" {
		return
	}
	slot, deadline, eligible := state.Policy.Window(now)
	if !eligible {
		reason := "等待维护窗口"
		if !state.Policy.ExpiresAt.After(now) {
			reason = "自动升级授权已到期"
		}
		if state.Reason != reason {
			state.Reason = reason
			_ = c.saveAutomation(state)
		}
		return
	}
	if now.Sub(state.LastCheck) < 15*time.Minute {
		return
	}
	state.LastCheck = now
	report := func(reason string) { state.Reason = reason; _ = c.saveAutomation(state) }
	// Persist the attempt before network activity to avoid a restart fetch loop.
	if c.saveAutomation(state) != nil {
		return
	}
	raw, e := systemupdate.Fetch(ctx)
	if e != nil {
		report(e.Error())
		return
	}
	catalog, _, e := systemupdate.Verify(raw, systemupdate.TrustedKeys(), now, 0, "")
	if e != nil {
		report(e.Error())
		return
	}
	target, ok := state.Policy.Select(catalog, buildmeta.Release)
	if !ok {
		report("授权范围内没有可升级版本")
		return
	}
	if state.Policy.Mode == "prepare" {
		if previous, e := systemupdate.ReadUpgradeTask(c.directory(), state.LastTask); e == nil && previous.Stage == "prepared" && previous.Plan.Target == target.Version && previous.Plan.Source == buildmeta.Release {
			report("该版本已完成自动准备；正式升级仍会重新预检")
			return
		}
	}
	slotKey := fmt.Sprintf("%d/%s/%s", state.Policy.Revision, slot, state.Policy.Mode)
	key := slotKey + "/" + target.Version
	if state.LastSlot == slotKey {
		return
	}
	digest := sha256.Sum256([]byte(key))
	id := hex.EncodeToString(digest[:16])
	e = c.Engine.Maintain(ctx, func(ctx context.Context) error {
		p, e := c.check(ctx, Request{ID: id, Version: target.Version, Actor: state.Actor, Envelope: raw})
		if e != nil {
			return e
		}
		p.PrepareOnly = state.Policy.Mode == "prepare"
		p.AutomationRevision = state.Policy.Revision
		p.StartBefore = &deadline
		p.AllowSchemaChange = state.Policy.AllowSchemaChange
		unlock, e := systemupdate.LockTransaction(c.Executor.Directory)
		if e != nil {
			return e
		}
		defer unlock()
		if e = c.prepareRunner(p); e != nil {
			return errors.New("无法保存独立自动升级进程")
		}
		// Reserve this slot before acceptance; a crash here may miss one window,
		// but must never repeat an uncertain destructive operation.
		state.LastSlot, state.LastTask, state.LastStage = slotKey, id, "queued"
		state.Reason = "已预约独立任务，等待宿主机执行"
		if e = c.saveAutomation(state); e != nil {
			return e
		}
		_, e = systemupdate.CreateTransaction(c.directory(), p, systemupdate.TrustedKeys())
		return e
	})
	if e != nil {
		report("自动升级未启动：" + e.Error())
		return
	}
	if e = c.Resume(ctx); e != nil {
		report(e.Error())
	}
}

func (c *Controller) RunAutomation(ctx context.Context) {
	timer := time.NewTicker(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			c.cleanupAutomationArtifacts(ctx, time.Now())
			call, cancel := context.WithTimeout(ctx, 55*time.Second)
			c.automationTick(call, time.Now().UTC())
			cancel()
		}
	}
}
