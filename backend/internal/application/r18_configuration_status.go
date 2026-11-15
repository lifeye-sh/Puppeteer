package application

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"puppeteer/backend/internal/modules/identity"
	"puppeteer/backend/internal/platform/configstate"
	"puppeteer/backend/internal/platform/deploytask"
	"puppeteer/backend/internal/platform/fault"
	"puppeteer/backend/internal/platform/guac"
	"puppeteer/backend/internal/platform/mp4probe"
)

type configurationHost struct {
	Components map[string]deploytask.ComponentStatus `json:"components"`
	Version    int                                   `json:"version"`
	Generation uint64                                `json:"generation"`
	Task       *deploytask.Task                      `json:"task"`
	History    []deploytask.Task                     `json:"history"`
	Mappings   map[string]*deploytask.Mapping        `json:"mappings"`
}

func (a *App) configurationHost(ctx context.Context) (*configurationHost, error) {
	status, raw, err := a.configurationHostCall(ctx, "GET", "/v1/status", nil)
	if err != nil || status != 200 {
		return nil, errors.New("executor_unavailable")
	}
	var host configurationHost
	if json.Unmarshal(raw, &host) != nil || host.Version != 1 || host.Mappings == nil {
		return nil, errors.New("executor_capability_missing")
	}
	return &host, nil
}

func (a *App) configurationHostCall(ctx context.Context, method, path string, input any) (int, json.RawMessage, error) {
	if a.config.DeploySocket == "" {
		return 0, nil, errors.New("executor_not_enrolled")
	}
	token, err := os.ReadFile(a.config.DeployTokenFile)
	if err != nil {
		return 0, nil, errors.New("executor_unavailable")
	}
	defer clear(token)
	return (deploytask.Client{Socket: a.config.DeploySocket, Token: strings.TrimSpace(string(token))}).Call(ctx, method, path, input)
}

func latestConfigurationDeployment(host *configurationHost, protocol string) *deploytask.Task {
	if host == nil {
		return nil
	}
	var last *deploytask.Task
	for i := range host.History {
		t := &host.History[i]
		if t.Request.Protocol == protocol && (last == nil || t.Started.After(last.Started)) {
			last = t
		}
	}
	if host.Task != nil && host.Task.Request.Protocol == protocol && (last == nil || !host.Task.Started.Before(last.Started)) {
		last = host.Task
	}
	return last
}

type configurationDeployment struct {
	ID      string    `json:"id"`
	State   string    `json:"state"`
	Stage   string    `json:"stage"`
	Actor   string    `json:"actor"`
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
}

type configurationDimension struct {
	State  string `json:"state"`
	Scope  string `json:"scope"`
	Reason string `json:"reason,omitempty"`
}

type configurationFeature struct {
	Capability *nativeRDPCapability `json:"capability,omitempty"`
	configstate.Feature
	Desired        configurationDimension   `json:"desired"`
	Applied        configurationDimension   `json:"applied"`
	Dependencies   configurationDimension   `json:"dependencies"`
	Service        configurationDimension   `json:"service"`
	Deployment     *configurationDeployment `json:"deployment,omitempty"`
	CheckSupported bool                     `json:"checkSupported"`
	LastCheck      *configstate.Task        `json:"lastCheck,omitempty"`
}

func (a *App) registerConfigurationStatus(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("GET /api/settings/operations", a.configurationStatus)
	h("GET /api/settings/operations/{feature}", a.resourceDependencyStatus)
	h("POST /api/settings/operations/{feature}/preflight", a.configurationCheckStart)
	h("POST /api/settings/operations/{feature}/verify", a.configurationCheckStart)
	h("GET /api/settings/operation-tasks", a.configurationCheckHistory)
	h("POST /api/settings/operation-tasks/{id}/cancel", a.configurationCheckCancel)
}

func (a *App) configurationChecks(ctx context.Context, limit int) ([]configstate.Task, error) {
	rows, err := a.db.Rows(ctx, `SELECT payload FROM configuration_checks ORDER BY started_at DESC,id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []configstate.Task{}
	for rows.Next() {
		var raw []byte
		var t configstate.Task
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &t); err != nil {
			return nil, err
		}
		items = append(items, configstate.Normalize(t, time.Now()))
	}
	return items, rows.Err()
}

func (a *App) configurationStatus(w http.ResponseWriter, r *http.Request) error {
	if _, err := a.require(r, "settings.read"); err != nil {
		return err
	}
	view, err := a.system.ConfigurationView(r.Context(), 0)
	if err != nil {
		return err
	}
	// One record per feature, so a busy feature cannot hide another's evidence.
	rows, err := a.db.Rows(r.Context(), `SELECT DISTINCT ON(feature) payload FROM configuration_checks ORDER BY feature,(node_id=$1) DESC,started_at DESC,id DESC`, a.configurationNode)
	if err != nil {
		return err
	}
	latest := map[string]*configstate.Task{}
	for rows.Next() {
		var raw []byte
		var t configstate.Task
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &t); err != nil {
			rows.Close()
			return err
		}
		t = configstate.Normalize(t, time.Now())
		latest[t.Feature] = &t
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	host, hostErr := a.configurationHost(ctx)
	var generation *uint64
	if hostErr == nil {
		generation = &host.Generation
	}
	active := map[string]bool{}
	known := map[string]bool{}
	a.configurationMu.Lock()
	if a.listenerManager != nil {
		for _, name := range []string{"mysql", "postgres", "ssh", "native-rdp"} {
			known[name] = true
		}
		for _, s := range a.listenerManager.States() {
			name := s.Name
			if name == "rdp" {
				name = "native-rdp"
			}
			active[name] = true
		}
	}
	if a.publicListeners != nil {
		known["management"], known["website"] = true, true
		for _, s := range a.publicListeners.States() {
			name := s.Name
			if name == "http" {
				name = "management"
			}
			active[name] = true
		}
	}
	a.configurationMu.Unlock()
	items := []configurationFeature{}
	now := time.Now().UTC()
	for _, f := range configstate.Features {
		desired := "configured"
		if f.Key != "" {
			if _, ok := view.Values[f.Key]; !ok {
				continue
			}
			enabled := view.Values.Bool(f.Key)
			if f.Invert {
				enabled = !enabled
			}
			desired = "disabled"
			if enabled {
				desired = "enabled"
			}
		}
		if f.ID == "management" {
			desired = "enabled"
		}
		if f.ID == "graphics" {
			desired = "disabled"
			if a.config.GuacdAddr != "" {
				desired = "enabled"
			}
		}
		applied := map[string]string{"requested": "pending", "applying": "applying", "effective": "applied", "failed": "failed", "stalled": "failed"}[view.State]
		if applied == "" {
			applied = "pending"
		}
		item := configurationFeature{Feature: f, Desired: configurationDimension{State: desired, Scope: "configuration"}, Applied: configurationDimension{State: applied, Scope: "configuration-nodes"}, Dependencies: configurationDimension{State: "unknown", Scope: "node", Reason: "尚无当前配置的检测证据"}, Service: configurationDimension{State: "unknown", Scope: "node", Reason: "尚未接入实际服务探测"}, CheckSupported: f.Protocol != "", LastCheck: latest[f.ID]}
		if f.Protocol != "" {
			if managed, ok := a.config.ManagedDeploymentProtocols[f.Protocol]; ok {
				item.Desired.Scope = "host-deployment"
				item.Desired.State = "disabled"
				if managed {
					item.Desired.State = "enabled"
				}
			}
			if host != nil {
				last := latestConfigurationDeployment(host, f.Protocol)
				if last != nil {
					item.Deployment = &configurationDeployment{ID: last.ID, State: last.State, Stage: last.Stage, Actor: last.Request.Actor, Started: last.Started, Updated: last.Updated}
					item.Desired.Scope = "host-deployment"
					item.Desired.State = "disabled"
					if last.Request.Enabled {
						item.Desired.State = "enabled"
					}
					item.Applied.Scope = "host-deployment"
					item.Applied.State = map[string]string{"running": "applying", "succeeded": "applied", "failed": "failed"}[last.State]
				}
			}
			item.Dependencies.State = configstate.Dependency(item.LastCheck, a.configurationNode, view.ID, generation, now)
			if item.Dependencies.State == "ready" {
				item.Dependencies.Reason = "当前节点和部署版本的预检通过；不代表公网可达"
			}
			if item.Dependencies.State == "stale" {
				item.Dependencies.Reason = "配置、节点、部署版本或检测有效期已变化，请重新预检"
			}
			if item.Dependencies.State == "blocked" && item.LastCheck != nil && len(item.LastCheck.Checks) > 0 {
				item.Dependencies.Reason = item.LastCheck.Checks[len(item.LastCheck.Checks)-1].Summary
			}
			if item.Dependencies.State == "checking" {
				item.Dependencies.Reason = "只读预检正在执行"
			}
			if hostErr != nil {
				item.Dependencies.State = "blocked"
				item.Dependencies.Reason = "宿主执行器未接入、不可达或不支持状态协议"
			}
			if known[f.ID] {
				item.Service.State = "stopped"
				item.Service.Reason = "当前节点未监听"
				if active[f.ID] {
					item.Service.State = "running"
					item.Service.Reason = "当前节点已监听；不代表目标资产或公网可用"
					if f.ID != "management" {
						confirmed := false
						for _, node := range view.Nodes {
							if node.ID == a.configurationNode && node.Online && node.State == "effective" && node.LastAppliedRevision == view.ID {
								confirmed = true
							}
						}
						if !confirmed {
							item.Service.State = "degraded"
							item.Service.Reason = "监听存在，但当前节点尚未确认最新配置，不能判定新连接可用"
						}
					}
				}
			}
		}
		if f.ID == "graphics" {
			component, ok := deploytask.ComponentStatus{}, false
			if host != nil {
				component, ok = host.Components["graphics"]
			}
			item.CheckSupported = ok && component.Supported
			if !item.CheckSupported {
				item.Dependencies = configurationDimension{State: "blocked", Scope: "host", Reason: "图形组件自动部署尚未接管或执行器版本不支持"}
				if component.Reason != "" {
					item.Dependencies.Reason = component.Reason
				}
			}
			if a.config.GuacdAddr == "" {
				item.Service = configurationDimension{State: "unknown", Scope: "node", Reason: "应用已停止接收新图形连接；无法确认组件停止状态"}
				if ok && component.Supported {
					item.Service = configurationDimension{State: component.State, Scope: "host-graphics", Reason: component.Reason}
					if component.State == "running" {
						item.Service.State, item.Service.Reason = "degraded", "应用已停止接收新图形连接，但组件仍在运行"
					}
				}
			} else {
				probeCtx, stop := context.WithTimeout(r.Context(), 2*time.Second)
				probeErr := guac.Probe(probeCtx, a.config.GuacdAddr)
				stop()
				item.Service = configurationDimension{State: "running", Scope: "node-guacd-handshake", Reason: "当前节点到 guacd 的 RDP/VNC 插件握手通过；不代表目标资产可用"}
				if probeErr != nil {
					item.Service.State = "failed"
					item.Service.Reason = "图形组件连接或协议握手失败"
				}
			}
		}
		if f.ID == "rdp-drive" {
			item.Desired = configurationDimension{State: "disabled", Scope: "host-deployment"}
			if a.config.RDPDriveEnabled {
				item.Desired.State = "enabled"
			}
			item.CheckSupported = false
			item.Dependencies = configurationDimension{State: "blocked", Scope: "host", Reason: "共享盘部署尚未接管或执行器版本不支持"}
			item.Service = configurationDimension{State: "unknown", Scope: "host-mount", Reason: "尚无实际挂载证据"}
			if host != nil {
				if c, ok := host.Components[f.ID]; ok && c.Supported {
					item.CheckSupported = true
					if c.Enabled {
						item.Desired.State = "enabled"
					} else {
						item.Desired.State = "disabled"
					}
					item.Dependencies.State = configstate.Dependency(item.LastCheck, a.configurationNode, view.ID, generation, now)
					item.Dependencies.Reason = "以当前版本的只读预检为准；允许文件传输不代表共享盘已挂载"
					if item.Dependencies.State == "blocked" && item.LastCheck != nil && len(item.LastCheck.Checks) > 0 {
						item.Dependencies.Reason = item.LastCheck.Checks[len(item.LastCheck.Checks)-1].Summary
					}
					item.Service = configurationDimension{State: c.State, Scope: "host-mount", Reason: c.Reason}
					if last := latestConfigurationDeployment(host, f.Protocol); last != nil {
						if last.Request.Enabled {
							item.Desired.State = "enabled"
						} else {
							item.Desired.State = "disabled"
						}
					}
				} else if c.Reason != "" {
					item.Dependencies.Reason = c.Reason
				}
			}
		}

		if f.ID == "mp4" {
			item.Desired = configurationDimension{State: "disabled", Scope: "host-deployment"}
			if a.config.MP4URL != "" {
				item.Desired.State = "enabled"
			}
			item.CheckSupported = false
			item.Service = configurationDimension{State: "unknown", Scope: "host-renderer", Reason: "尚无渲染器运行证据"}
			if host != nil {
				if c, ok := host.Components[f.ID]; ok && c.Supported {
					item.CheckSupported = true
					if c.Enabled {
						item.Desired.State = "enabled"
					} else {
						item.Desired.State = "disabled"
					}
					item.Service = configurationDimension{State: c.State, Scope: "host-renderer", Reason: c.Reason}
					if c.State == "running" && a.config.MP4URL == "" {
						item.Service.State = "degraded"
						item.Service.Reason = "应用已暂停转换，但渲染器仍在运行"
					}
				}
				if last := latestConfigurationDeployment(host, f.Protocol); last != nil {
					if last.Request.Enabled {
						item.Desired.State = "enabled"
					} else {
						item.Desired.State = "disabled"
					}
				}
			}
			if !item.CheckSupported {
				item.Dependencies = configurationDimension{State: "blocked", Scope: "host", Reason: "MP4 组件未接管或执行器版本不支持"}
			}
			if a.config.MP4URL != "" {
				probeCtx, stop := context.WithTimeout(r.Context(), 2*time.Second)
				err := mp4probe.Ready(probeCtx, a.config.MP4URL, a.config.MP4Token)
				stop()
				if err != nil {
					item.Service = configurationDimension{State: "failed", Scope: "node-renderer", Reason: "MP4 服务连接、协议或认证失败"}
				} else if item.Service.State == "running" {
					item.Service.Reason = "容器健康且当前节点认证通过；转换能力以部署时样例验证为准"
				}
			}
		}

		if f.ID == "native-rdp" {
			capability := a.inspectNativeRDP(view.Values)
			item.Capability = &capability
			item.CheckSupported = true
			item.Dependencies = configurationDimension{State: configstate.Dependency(item.LastCheck, a.configurationNode, view.ID, nil, now), Scope: "node-rdp-capability", Reason: "运行能力由显式预检确认；不依赖宿主部署执行器"}
			switch item.Dependencies.State {
			case "stale":
				item.Dependencies.Reason = "检测记录已过期，或配置/节点已变化，请重新预检"
			case "checking":
				item.Dependencies.Reason = "原生 RDP 能力预检正在执行"
			case "ready":
				item.Dependencies.Reason = "当前节点和配置的能力预检通过；不代表目标会话可用"
			}
			if item.LastCheck != nil && item.LastCheck.Evidence != capability.Evidence {
				item.Dependencies.State = "stale"
				item.Dependencies.Reason = "Worker、编码器、证书或部署参数已变化，请重新预检"
			}
			if item.Dependencies.State == "blocked" && item.LastCheck != nil {
				for _, c := range item.LastCheck.Checks {
					if c.State == "blocked" {
						item.Dependencies.Reason = c.Summary
						break
					}
				}
			}
			if !capability.StaticReady {
				item.Dependencies.State = "blocked"
				for _, c := range capability.Checks {
					if c.State == "blocked" {
						item.Dependencies.Reason = c.Summary
						break
					}
				}
			}
			item.Service = configurationDimension{State: "unknown", Scope: "node-rdp-listener", Reason: "监听管理器尚未就绪"}
			if known[f.ID] {
				item.Service.State = "stopped"
				item.Service.Reason = "当前节点没有原生 RDP 监听"
			}
			if active[f.ID] {
				item.Service.State = "running"
				item.Service.Reason = "当前节点 RDP 监听存在；不代表目标资产认证、完整录屏或公网可用"
				confirmed := false
				for _, node := range view.Nodes {
					if node.ID == a.configurationNode && node.Online && node.State == "effective" && node.LastAppliedRevision == view.ID {
						confirmed = true
					}
				}
				if !confirmed || !capability.StaticReady {
					item.Service.State = "degraded"
					item.Service.Reason = "监听仍存在，但当前配置未确认或部署依赖/容量不满足新连接要求"
				}
			}
		}
		if externalCheckSupported(f.ID) {
			item.CheckSupported = true
			item.Dependencies.State = configstate.Dependency(item.LastCheck, a.configurationNode, view.ID, nil, now)
			if item.LastCheck != nil {
				current, _, e := a.dependencyEvidence(r.Context(), view.Values, f.ID, item.LastCheck.Actor)
				if e != nil || item.LastCheck.Evidence != current {
					item.Dependencies.State = "stale"
				}
			}
			item.Dependencies.Reason = map[string]string{"unknown": "尚未检测当前节点的外部依赖", "checking": "正在验证已保存配置的外部依赖", "ready": "限定范围的依赖预检通过，实际投递、授权登录或设备注册需单独验证", "blocked": "外部依赖预检未通过，请查看分项原因", "stale": "配置、节点或检测有效期变化，请重新预检"}[item.Dependencies.State]
			item.Service = configurationDimension{State: "unknown", Scope: "business-verification", Reason: "依赖连接不代表实际业务成功；请在对应功能中显式验证"}
			if desired == "disabled" {
				item.Service = configurationDimension{State: "stopped", Scope: "configuration-policy", Reason: "功能期望关闭；外部服务本身未由平台启停"}
			}
		}
		if f.Category == "policies" {
			item.Dependencies = configurationDimension{State: "not_applicable", Scope: "policy"}
			item.Service = configurationDimension{State: "not_applicable", Scope: "policy", Reason: "按配置发布结果及各自生效时机执行"}
		}
		items = append(items, item)
	}
	// Never forward the host's progress bearer token, raw errors or configuration values.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"version": 1, "revision": view.ID, "actor": view.Actor, "node": a.configurationNode, "checkedAt": now, "generation": generation, "nodes": view.Nodes, "securityPending": view.SecurityPending, "items": items})
	return nil
}

func (a *App) configurationCheckAdmin(r *http.Request) (*identity.User, error) {
	u, err := a.require(r, "identity.manage")
	if err != nil {
		return nil, err
	}
	if u.Type != "admin" {
		return nil, fault.Denied
	}
	return u, nil
}

func (a *App) configurationCheckStart(w http.ResponseWriter, r *http.Request) error {
	u, err := a.configurationCheckAdmin(r)
	if err != nil {
		return err
	}
	f, ok := configurationOperationFeature(r.PathValue("feature"))
	if !ok {
		return problem(404, "operation_not_found", "未找到配置项或预检任务")
	}
	if f.Protocol == "" && f.ID != "native-rdp" && !externalCheckSupported(f.ID) {
		return problem(422, "check_not_supported", "此项尚未接入统一预检，请使用对应功能的专项检测")
	}
	var body struct {
		Evidence string `json:"evidence,omitempty"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	expected, err := configMatch(r, true)
	if err != nil {
		return err
	}
	mode := "preflight"
	if strings.HasSuffix(r.URL.Path, "/verify") {
		mode = "verify"
	}
	resourceKind, target := dependencyResource(f.ID)
	if mode == "verify" && resourceKind != "backup-destination" {
		return problem(422, "verification_not_supported", "此资源不支持此项显式验证")
	}
	if resourceKind == "backup-destination" {
		if _, e := a.require(r, "backup.manage"); e != nil {
			return e
		}
	}
	if target != "" && len(body.Evidence) != 64 {
		return problem(409, "resource_check_required", "请刷新资源状态后再预检")
	}
	if target == "" && body.Evidence != "" {
		return fault.Invalid
	}
	now := time.Now().UTC()
	t := configstate.Task{Mode: mode, Evidence: body.Evidence, ID: identity.NewToken()[:32], Feature: f.ID, Node: a.configurationNode, Revision: *expected, Actor: u.ID, State: "running", Stage: "queued", Started: now, Updated: now, Expires: now.Add(5 * time.Minute), Deadline: now.Add(45 * time.Second), Checks: []configstate.Check{}}
	reused := false
	err = a.db.Within(r.Context(), func(ctx context.Context) error {
		// Serialize admissions across nodes without holding a transaction during probes.
		if _, e := a.db.Exec(ctx, `SELECT pg_advisory_xact_lock(78201801)`); e != nil {
			return e
		}
		var revision int64
		if e := a.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR SHARE`).Scan(&revision); e != nil {
			return e
		}
		if revision != *expected {
			return fault.Conflict
		}
		if _, e := a.db.Exec(ctx, `UPDATE configuration_checks SET state='stale',payload=jsonb_set(jsonb_set(payload,'{state}','"interrupted"'),'{stage}','"interrupted"') WHERE state='running' AND deadline<=now()`); e != nil {
			return e
		}
		var raw []byte
		e := a.db.Row(ctx, `SELECT payload FROM configuration_checks WHERE state='running' AND deadline>now() ORDER BY started_at DESC LIMIT 1`).Scan(&raw)
		if e == nil {
			var active configstate.Task
			if json.Unmarshal(raw, &active) != nil {
				return fault.Invalid
			}
			if active.Feature == f.ID && active.Node == t.Node && active.Revision == t.Revision && active.Mode == t.Mode && (target == "" || active.Evidence == t.Evidence) {
				t = active
				reused = true
				return nil
			}
			return problem(409, "check_busy", "已有预检正在执行，请等待完成或取消")
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		raw, _ = json.Marshal(t)
		if _, e = a.db.Exec(ctx, `INSERT INTO configuration_checks(id,feature,node_id,revision,actor_id,state,deadline,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, t.ID, t.Feature, t.Node, t.Revision, t.Actor, t.State, t.Deadline, raw); e != nil {
			return e
		}
		// Retain the latest evidence per feature and at least the newest 200 tasks.
		if _, e = a.db.Exec(ctx, `DELETE FROM configuration_checks WHERE id IN (SELECT id FROM configuration_checks ORDER BY started_at DESC OFFSET 200) AND id NOT IN (SELECT DISTINCT ON(feature) id FROM configuration_checks ORDER BY feature,started_at DESC) AND state<>'running' AND id NOT IN (SELECT task_id FROM dependency_probe_artifacts)`); e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "configuration."+mode, requestID(r), sourceIP(r), map[string]string{"feature": f.ID, "task": t.ID, "revision": strconv.FormatInt(t.Revision, 10)})
	})
	if err != nil {
		return err
	}
	if !reused {
		go a.runConfigurationCheck(t, f)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 202, t)
	return nil
}

func (a *App) storeConfigurationCheck(ctx context.Context, t configstate.Task) bool {
	for i := range t.Checks {
		if t.Checks[i].CheckedAt.IsZero() {
			t.Checks[i].CheckedAt = t.Updated
		}
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return false
	}
	result, err := a.db.Exec(ctx, `UPDATE configuration_checks SET state=$2,payload=$3 WHERE id=$1 AND state='running' AND deadline>now()`, t.ID, t.State, raw)
	return err == nil && result.RowsAffected() == 1
}

func (a *App) runConfigurationCheck(t configstate.Task, f configstate.Feature) {
	ctx, cancel := context.WithDeadline(context.Background(), t.Deadline.Add(-time.Second))
	defer cancel()
	finish := func(code, message, remedy string) {
		t.State = "failed"
		t.Stage = "complete"
		t.Updated = time.Now().UTC()
		t.Checks = append(t.Checks, configstate.Check{Code: code, State: "blocked", Summary: message, Remediation: remedy, Scope: "node"})
		a.storeConfigurationCheck(ctx, t)
	}
	select {
	case a.probeSlots <- struct{}{}:
		defer func() { <-a.probeSlots }()
	default:
		finish("check_capacity", "只读检测并发数已满", "等待在途检测结束后重试")
		return
	}
	t.Stage = "application"
	t.Updated = time.Now().UTC()
	if !a.storeConfigurationCheck(ctx, t) {
		return
	}
	revision, err := a.system.Configuration(ctx, 0)
	if err != nil {
		finish("configuration_unavailable", "无法读取配置", "恢复数据库连接后重试")
		return
	}
	if revision.ID != t.Revision {
		finish("configuration_changed", "配置已变化", "刷新后重新预检")
		return
	}
	if f.ID == "native-rdp" {
		a.runNativeRDPCheck(ctx, t, revision.Values)
		return
	}
	if externalCheckSupported(f.ID) {
		a.runExternalDependencyCheck(ctx, t, revision.Values)
		return
	}
	host, err := a.configurationHost(ctx)
	if err != nil {
		finish("executor_unavailable", "宿主执行器未接入、不可达或版本不支持", "检查安装升级接管状态和执行器服务")
		return
	}
	t.Generation = &host.Generation
	if last := latestConfigurationDeployment(host, f.Protocol); last != nil && last.State == "failed" {
		finish("deployment_not_applied", "上次部署失败，用户期望尚未完成应用", "在服务部署页面处理失败任务后重新预检")
		return
	}
	if host.Task != nil && host.Task.State == "running" {
		finish("deployment_busy", "部署正在执行", "等待部署完成后重新检测")
		return
	}
	enabled := revision.Values.Bool(f.Key)
	if f.ID == "management" {
		enabled = true
	}
	if managed, ok := a.config.ManagedDeploymentProtocols[f.Protocol]; ok {
		enabled = managed
	}
	if f.ID == "graphics" || f.ID == "rdp-drive" || f.ID == "mp4" {
		component, ok := host.Components[f.ID]
		if !ok || !component.Supported {
			finish("component_not_enrolled", "组件未接管或执行器版本不支持", "使用匹配的安装升级脚本接管内部组件")
			return
		}
		enabled = component.Enabled
	}
	req := deploytask.Request{Protocol: f.Protocol, Enabled: enabled, Generation: host.Generation}
	if f.ID == "management" {
		specs := a.publicListenerSpecs(revision.Values)
		if len(specs) > 0 {
			req.Bind = "0.0.0.0"
			_, port, splitErr := net.SplitHostPort(specs[0].Address)
			if splitErr == nil {
				req.Port, _ = strconv.Atoi(port)
			}
		}
	} else if mapping := host.Mappings[f.Protocol]; mapping != nil {
		req.Bind = mapping.Bind
		req.Port = mapping.Published
	}
	if deploytask.Validate(req) != nil {
		finish("deployment_target_missing", "当前配置缺少可验证的部署地址或映射", "在服务部署页面填写端口并执行预检及部署")
		return
	}
	if a.DeploymentApplicationCheck(ctx, req) != nil {
		finish("application_dependency_failed", "证书、网站配置或上游依赖检查失败", "检查对应服务的证书和网站上游，在部署页面查看专项原因")
		return
	}
	t.Checks = append(t.Checks, configstate.Check{Code: "application_checked", State: "ready", Summary: "当前配置的应用侧检查通过", Scope: "node"})
	t.Stage = "host"
	t.Updated = time.Now().UTC()
	if !a.storeConfigurationCheck(ctx, t) {
		return
	}
	status, raw, err := a.configurationHostCall(ctx, "POST", "/v1/preflight", req)
	var result struct {
		Passed bool   `json:"passed"`
		Error  string `json:"error"`
	}
	if err != nil || json.Unmarshal(raw, &result) != nil || status != 200 || !result.Passed {
		code, message := "host_preflight_failed", "宿主预检失败或执行器暂不可用"
		if status == 409 {
			code, message = "deployment_changed", "部署版本已变化或已有部署正在执行"
		} else if strings.Contains(strings.ToLower(result.Error), "port") || strings.Contains(result.Error, "端口") {
			code, message = "port_check_failed", "端口占用或端口映射检查失败"
		}
		finish(code, message, "检查部署页面的端口、映射和执行器状态后重试")
		return
	}
	latest, err := a.system.Configuration(ctx, 0)
	if err != nil || latest.ID != t.Revision {
		finish("configuration_changed", "预检期间配置已变化", "刷新后重新预检")
		return
	}
	current, err := a.configurationHost(ctx)
	if err != nil || current.Generation != host.Generation || current.Task != nil && current.Task.State == "running" {
		finish("deployment_changed", "预检期间部署状态已变化", "等待部署结束后重新预检")
		return
	}
	t.Checks = append(t.Checks, configstate.Check{Code: "host_checked", State: "ready", Summary: "当前宿主配置与端口预检通过", Scope: "host"})
	t.State = "succeeded"
	t.Stage = "complete"
	t.Updated = time.Now().UTC()
	t.Expires = t.Updated.Add(5 * time.Minute)
	a.storeConfigurationCheck(ctx, t)
}

func (a *App) configurationCheckHistory(w http.ResponseWriter, r *http.Request) error {
	if _, err := a.require(r, "settings.read"); err != nil {
		return err
	}
	items, err := a.configurationChecks(r.Context(), 100)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"items": items})
	return nil
}

func (a *App) configurationCheckCancel(w http.ResponseWriter, r *http.Request) error {
	u, err := a.configurationCheckAdmin(r)
	if err != nil {
		return err
	}
	err = a.db.Within(r.Context(), func(ctx context.Context) error {
		var raw []byte
		if e := a.db.Row(ctx, `SELECT payload FROM configuration_checks WHERE id=$1 FOR UPDATE`, r.PathValue("id")).Scan(&raw); errors.Is(e, pgx.ErrNoRows) {
			return problem(404, "operation_not_found", "未找到配置项或预检任务")
		} else if e != nil {
			return e
		}
		var t configstate.Task
		if json.Unmarshal(raw, &t) != nil {
			return fault.Invalid
		}
		if t.State != "running" {
			return nil
		}
		if t.Mode == "verify" {
			return problem(409, "verification_cleanup_required", "显式验证须完成测试对象清理，当前阶段不可取消")
		}
		t.State, t.Stage = "cancelled", "cancelled"
		t.Updated = time.Now().UTC()
		t.Checks = append(t.Checks, configstate.Check{Code: "check_cancelled", State: "blocked", Summary: "已取消预检；在途只读探测可能持续到超时，其结果将被丢弃", Scope: "node"})
		raw, _ = json.Marshal(t)
		if _, e := a.db.Exec(ctx, `UPDATE configuration_checks SET state='cancelled',payload=$2 WHERE id=$1`, t.ID, raw); e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "configuration.preflight.cancel", requestID(r), sourceIP(r), map[string]string{"task": t.ID})
	})
	if err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}
