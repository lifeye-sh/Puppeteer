package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"puppeteer/backend/internal/modules/identity"
	"puppeteer/backend/internal/platform/deploytask"
	"puppeteer/backend/internal/platform/fault"
	"puppeteer/backend/internal/platform/systemupdate"
	"strings"
	"time"
)

func (a *App) updates() (*systemupdate.Manager, error) {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	if a.updateManager != nil {
		return a.updateManager, nil
	}
	path := ""
	if a.config.BackupDirectory != "" {
		path = filepath.Join(filepath.Dir(a.config.BackupDirectory), "runtime", "system-updates.json")
	}
	m, e := systemupdate.New(path, systemupdate.TrustedKeys(), nil)
	if e == nil {
		a.updateManager = m
	}
	return m, e
}
func (a *App) updateInventory(ctx context.Context) (map[string]any, error) {
	token, e := os.ReadFile(a.config.DeployTokenFile)
	if e != nil || a.config.DeploySocket == "" {
		return nil, problem(503, "deployment_not_enrolled", "部署执行器尚未接入")
	}
	code, body, e := (deploytask.Client{Socket: a.config.DeploySocket, Token: strings.TrimSpace(string(token))}).Call(ctx, "GET", "/v1/updates/inventory", nil)
	if e != nil || code != 200 {
		return nil, problem(503, "update_inventory_unavailable", "执行器不可达或尚不支持系统更新预检，请升级匹配的执行器")
	}
	var out map[string]any
	if json.Unmarshal(body, &out) != nil {
		return nil, fault.Invalid
	}
	return out, nil
}

type updateCheck struct {
	Key    string `json:"key"`
	Passed bool   `json:"passed"`
	Reason string `json:"reason"`
}

func (a *App) updateCall(ctx context.Context, method, path string, input any) (int, json.RawMessage, error) {
	token, e := os.ReadFile(a.config.DeployTokenFile)
	if e != nil {
		return 0, nil, problem(503, "deployment_not_enrolled", "部署执行器尚未接入")
	}
	code, body, e := (deploytask.Client{Socket: a.config.DeploySocket, Token: strings.TrimSpace(string(token))}).Call(ctx, method, path, input)
	if e != nil {
		return 0, nil, problem(503, "update_runner_unavailable", "升级执行器暂不可达，请查询任务状态，勿重复创建任务")
	}
	if code != 200 && code != 202 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &failure)
		if failure.Error == "" {
			failure.Error = "执行器尚不支持此升级操作"
		}
		return 0, nil, problem(code, "update_runner_rejected", failure.Error)
	}
	return code, body, nil
}

type upgradeSubmission struct {
	backupProof
	ID            string `json:"id"`
	Version       string `json:"version"`
	LayoutDigest  string `json:"layoutDigest"`
	Generation    uint64 `json:"generation"`
	ConfirmedStop bool   `json:"confirmedStop"`
}

func (a *App) registerSystemUpdates(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	a.registerUpgradeControl(h)
	admin := func(r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		if u.Type != "admin" {
			return fault.Denied
		}
		return nil
	}
	h("GET /api/admin/updates/task", func(w http.ResponseWriter, r *http.Request) error {
		if e := admin(r); e != nil {
			return e
		}
		code, body, e := a.updateCall(r.Context(), "GET", "/v1/updates/task", nil)
		if e != nil {
			return e
		}
		writeJSON(w, code, body)
		return nil
	})
	a.registerUpdateAutomation(h, admin)
	h("POST /api/admin/updates/tasks", func(w http.ResponseWriter, r *http.Request) error {
		if e := admin(r); e != nil {
			return e
		}
		var in upgradeSubmission
		if e := decode(w, r, &in); e != nil {
			return e
		}
		id, e := hex.DecodeString(in.ID)
		layout, le := hex.DecodeString(in.LayoutDigest)
		if e != nil || le != nil || len(id) != 16 || len(layout) != 32 || !in.ConfirmedStop {
			return problem(422, "update_confirmation_required", "请重新预检并确认升级期间停止会话和业务写入")
		}
		m, e := a.updates()
		if e != nil {
			return e
		}
		target, envelope, e := m.SignedTarget(in.Version)
		if e != nil {
			return problem(422, "update_target_invalid", e.Error())
		}
		state, e := a.system.State(r.Context())
		if e != nil {
			return e
		}
		cmp, ce := systemupdate.Compare(target.Version, Release)
		minimum, me := systemupdate.Compare(Release, target.MinSource)
		if ce != nil || me != nil || cmp <= 0 || minimum < 0 || target.SchemaVersion < state.SchemaVersion {
			return problem(422, "update_path_invalid", "版本或数据库状态已变化，请重新预检")
		}
		actor := ""
		if e = a.withPasswordProof(r, in.backupProof, "identity.manage", func(ctx context.Context, u *identity.User) error {
			if u.Type != "admin" {
				return fault.Denied
			}
			actor = u.ID
			return a.audit.Detail(ctx, u.ID, "system.upgrade_requested", requestID(r), sourceIP(r), map[string]string{"id": in.ID, "target": target.Version})
		}); e != nil {
			return e
		}
		code, body, e := a.updateCall(r.Context(), "POST", "/v1/updates/tasks", map[string]any{"id": in.ID, "version": in.Version, "actor": actor, "envelope": envelope, "layoutDigest": in.LayoutDigest, "generation": in.Generation})
		if e != nil {
			return e
		}
		writeJSON(w, code, body)
		return nil
	})
	h("GET /api/admin/updates", func(w http.ResponseWriter, r *http.Request) error {
		if e := admin(r); e != nil {
			return e
		}
		m, e := a.updates()
		if e != nil {
			return e
		}
		s, e := a.system.State(r.Context())
		if e != nil {
			return e
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"currentVersion": Release, "schemaVersion": s.SchemaVersion, "capabilitiesRevision": CapabilitiesRevision, "updates": m.View(Release), "automationEndpoint": "/api/admin/updates/automation"})
		return nil
	})
	h("GET /api/admin/updates/inventory", func(w http.ResponseWriter, r *http.Request) error {
		if e := admin(r); e != nil {
			return e
		}
		v, e := a.updateInventory(r.Context())
		if e != nil {
			return e
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, v)
		return nil
	})
	h("POST /api/admin/updates/check", func(w http.ResponseWriter, r *http.Request) error {
		if e := admin(r); e != nil {
			return e
		}
		m, e := a.updates()
		if e != nil {
			return e
		}
		// Record intent first: audit failure must not dispatch a network check.
		if e = a.db.Within(r.Context(), func(ctx context.Context) error {
			if e := admin(r.Clone(ctx)); e != nil {
				return e
			}
			u, e := a.authenticated(r.Clone(ctx))
			if e != nil {
				return e
			}
			return a.audit.Detail(ctx, u.ID, "system.update_check", requestID(r), sourceIP(r), nil)
		}); e != nil {
			return e
		}
		if e = m.Check(r.Context()); e != nil {
			return problem(422, "update_check_failed", e.Error())
		}
		writeJSON(w, 200, m.View(Release))
		return nil
	})
	h("PUT /api/admin/updates/settings", func(w http.ResponseWriter, r *http.Request) error {
		if e := admin(r); e != nil {
			return e
		}
		var in systemupdate.Settings
		if e := decode(w, r, &in); e != nil {
			return e
		}
		m, e := a.updates()
		if e != nil {
			return e
		}
		// Filesystem settings are outside the SQL transaction; audit the attempt
		// before applying it, and report success only after the durable write.
		if e = a.db.Within(r.Context(), func(ctx context.Context) error {
			if e := admin(r.Clone(ctx)); e != nil {
				return e
			}
			u, e := a.authenticated(r.Clone(ctx))
			if e != nil {
				return e
			}
			return a.audit.Detail(ctx, u.ID, "system.update_settings_requested", requestID(r), sourceIP(r), map[string]string{"channel": in.Channel})
		}); e != nil {
			return e
		}
		if e = m.Configure(in); e != nil {
			return problem(409, "update_settings_changed", e.Error())
		}
		writeJSON(w, 200, m.View(Release))
		return nil
	})
	h("POST /api/admin/updates/preflight", func(w http.ResponseWriter, r *http.Request) error {
		if e := admin(r); e != nil {
			return e
		}
		var in struct {
			Version string `json:"version"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		m, e := a.updates()
		if e != nil {
			return e
		}
		target, envelope, e := m.SignedTarget(in.Version)
		if e != nil {
			return problem(422, "update_target_invalid", e.Error())
		}
		state, e := a.system.State(r.Context())
		if e != nil {
			return e
		}
		checks := []updateCheck{}
		add := func(k string, ok bool, reason string) { checks = append(checks, updateCheck{k, ok, reason}) }
		cmp, ce := systemupdate.Compare(target.Version, Release)
		add("version", ce == nil && cmp > 0, "目标必须高于当前版本；不允许通过更新入口降级")
		cmp, ce = systemupdate.Compare(Release, target.MinSource)
		add("path", ce == nil && cmp >= 0, "最低来源版本："+target.MinSource)
		add("schema", target.SchemaVersion >= state.SchemaVersion, "目标数据库版本不得低于当前数据库版本")
		observed, err := a.updateInventory(r.Context())
		add("executor", err == nil, "需要已接管且支持更新预检的部署执行器")
		if err == nil {
			raw, _ := json.Marshal(observed["inventory"])
			var inv deploytask.UpdateInventory
			json.Unmarshal(raw, &inv)
			add("platform", inv.Platform == target.Platform, "目标平台："+target.Platform+"；执行器平台："+inv.Platform)
			add("managed", inv.Managed, "需要受管 Compose 布局")
			add("systemd", inv.Systemd, "在线自升级首批支持宿主机 systemd 部署")
			add("idle", observed["busy"] == false, "不能与其他部署任务同时执行")
			for _, c := range inv.Components {
				if c.Disabled {
					continue
				}
				add("service_"+c.Service, c.State == "running" && c.Health == "healthy" && c.ConfiguredImage == c.RunningImage, "检查 "+c.Service+" 的运行状态、健康状态和配置一致性")
			}
			add("runner", inv.RunnerReady, inv.Reason)
		} else {
			add("runner", false, "独立升级器尚未接入；请使用受管升级脚本")
		}
		passed := true
		for _, c := range checks {
			passed = passed && c.Passed
		}
		id := make([]byte, 16)
		if _, e = rand.Read(id); e != nil {
			return e
		}
		requestID := hex.EncodeToString(id)
		var preview struct {
			Passed       bool   `json:"passed"`
			LayoutDigest string `json:"layoutDigest"`
			Generation   uint64 `json:"generation"`
		}
		if passed {
			u, e := a.require(r, "identity.manage")
			if e != nil {
				return e
			}
			_, body, e := a.updateCall(r.Context(), "POST", "/v1/updates/preflight", map[string]any{"id": requestID, "version": in.Version, "actor": u.ID, "envelope": envelope})
			if e != nil {
				add("host_preflight", false, e.Error())
				passed = false
			} else {
				if json.Unmarshal(body, &preview) != nil {
					return fault.Invalid
				}
				passed = preview.Passed && len(preview.LayoutDigest) == 64
				add("host_preflight", passed, "宿主机升级能力、当前版本、管理员权限和任务排空条件")
			}
		}
		writeJSON(w, 200, map[string]any{"id": requestID, "version": in.Version, "passed": passed, "scope": "submission", "checks": checks, "layoutDigest": preview.LayoutDigest, "generation": preview.Generation, "automaticInstall": false, "notice": "提交后先准备镜像和检查空间，通过后停止会话并制作配套备份；备份验证通过才切换版本"})
		return nil
	})
}
func (a *App) RunUpdateChecks(ctx context.Context) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if !a.maintenance.Load() {
				m, e := a.updates()
				if e == nil && m.Due() {
					c, cancel := context.WithTimeout(ctx, 25*time.Second)
					_ = m.Check(c)
					cancel()
				}
				c, cancel := context.WithTimeout(ctx, 10*time.Second)
				a.collectUpdateNotifications(c)
				cancel()
			}
			timer.Reset(time.Minute)
		}
	}
}
