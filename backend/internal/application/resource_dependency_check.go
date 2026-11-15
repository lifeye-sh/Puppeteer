package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"puppeteer/backend/internal/modules/resource"
	"puppeteer/backend/internal/modules/system"
	"puppeteer/backend/internal/platform/configstate"
	"puppeteer/backend/internal/platform/fault"
)

func dependencyResource(feature string) (kind, id string) {
	kind, id, ok := strings.Cut(feature, ":")
	if !ok || !resource.ValidID(id) {
		return "", ""
	}
	switch kind {
	case "notification-channel", "ldap-directory", "oidc-provider", "ai-profile", "backup-destination", "acme-order", "agent-gateway", "ssh-jump", "gateway-group", "network-proxy":
		return kind, id
	}
	return "", ""
}

func configurationOperationFeature(id string) (configstate.Feature, bool) {
	if f, ok := configstate.Find(id); ok {
		return f, true
	}
	kind, target := dependencyResource(id)
	if target == "" {
		return configstate.Feature{}, false
	}
	labels := map[string]string{"notification-channel": "通知渠道", "ldap-directory": "LDAP 目录", "oidc-provider": "身份提供方", "ai-profile": "模型连接", "backup-destination": "备份复制目标", "acme-order": "ACME 订单"}
	for k, v := range map[string]string{"agent-gateway": "Agent 网关", "ssh-jump": "SSH 跳板", "gateway-group": "网关组", "network-proxy": "网络代理"} {
		labels[k] = v
	}
	return configstate.Feature{ID: id, Label: labels[kind], Category: "dependencies", Section: kind}, true
}

// Only hashed versions leave this function. Resource credentials and settings
// never enter task payloads. Queries are fixed allowlisted SQL, not user input.
func (a *App) dependencyEvidence(ctx context.Context, v system.ConfigurationValues, feature, actor string) (string, bool, error) {
	base := a.externalCheckEvidence(v)
	kind, id := dependencyResource(feature)
	var version int64
	var related string
	enabled := false
	var err error
	switch kind {
	case "agent-gateway", "ssh-jump", "gateway-group", "network-proxy":
		switch kind {
		case "agent-gateway":
			err = a.db.Row(ctx, `SELECT credential_version,revoked_at IS NULL FROM agent_gateway_configs WHERE id=$1`, id).Scan(&version, &enabled)
		case "ssh-jump":
			err = a.db.Row(ctx, `SELECT version,enabled FROM ssh_jump_hosts WHERE id=$1`, id).Scan(&version, &enabled)
		case "gateway-group":
			err = a.db.Row(ctx, `SELECT version,enabled FROM gateway_groups WHERE id=$1`, id).Scan(&version, &enabled)
		case "network-proxy":
			err = a.db.Row(ctx, `SELECT version,enabled FROM network_proxies WHERE id=$1`, id).Scan(&version, &enabled)
		}
		if err == nil {
			related, err = a.connectivityDependencyEvidence(ctx)
		}
		if kind == "agent-gateway" {
			related += strconv.FormatBool(a.connectivity.AgentDialReady(id))
		}
	case "notification-channel":
		err = a.db.Row(ctx, `SELECT c.version,c.enabled, jsonb_build_array((SELECT coalesce(jsonb_agg(to_jsonb(b) ORDER BY user_id),'[]') FROM notification_bindings b WHERE channel_id=c.id),(SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY id),'[]') FROM notification_rules r WHERE c.id=ANY(channel_ids)))::text FROM notification_channels c WHERE id=$1`, id).Scan(&version, &enabled, &related)
	case "ldap-directory":
		err = a.db.Row(ctx, `SELECT version,enabled OR binding_enabled FROM ldap_identity_providers WHERE id=$1`, id).Scan(&version, &enabled)
	case "oidc-provider":
		err = a.db.Row(ctx, `SELECT version,enabled OR binding_enabled FROM oidc_identity_providers WHERE id=$1`, id).Scan(&version, &enabled)
	case "backup-destination":
		err = a.db.Row(ctx, `SELECT revision,enabled FROM backup_destinations WHERE id=$1`, id).Scan(&version, &enabled)
		var pending int
		if err == nil {
			err = a.db.Row(ctx, `SELECT count(*) FROM dependency_probe_artifacts WHERE resource_id=$1`, id).Scan(&pending)
		}
		related = a.config.BackupDirectory + ":" + strconv.Itoa(pending)
	case "acme-order":
		err = a.db.Row(ctx, `SELECT o.version,o.auto_renew,jsonb_build_array(a.version,p.version,o.status,(SELECT count(*) FROM acme_dns_records WHERE order_id=o.id))::text FROM certificate_orders o JOIN acme_accounts a ON a.id=o.account_id JOIN dns_providers p ON p.id=o.provider_id WHERE o.id=$1`, id).Scan(&version, &enabled, &related)
	case "ai-profile":
		err = a.db.Row(ctx, `SELECT version,enabled FROM ai_profiles WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&version, &enabled)
	}
	if err != nil {
		return "", false, err
	}
	var aiState any
	if feature == "aiModel" || kind == "ai-profile" {
		policy, e := a.aiService().Settings(ctx)
		if e != nil {
			return "", false, e
		}
		var used int64
		if e = a.db.Row(ctx, `SELECT coalesce(sum(charged_micros),0) FROM ai_usage_ledger WHERE user_id=$1 AND created_at>=date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, actor).Scan(&used); e != nil {
			return "", false, e
		}
		aiState = []any{policy.Version, used, time.Now().UTC().Format("2006-01-02"), a.aiService().RequireExecution(ctx) == nil}
	}
	accessState, e := a.accessDependencyEvidence(ctx, feature, actor)
	if e != nil {
		return "", false, e
	}
	raw, _ := json.Marshal([]any{base, kind, id, version, enabled, related, aiState, accessState})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), enabled, nil
}

func (a *App) resourceDependencyStatus(w http.ResponseWriter, r *http.Request) error {
	f, ok := configurationOperationFeature(r.PathValue("feature"))
	kind, id := dependencyResource(f.ID)
	if !ok || id == "" {
		return problem(404, "operation_not_found", "未找到资源预检项")
	}
	permission := "identity.manage"
	if kind == "backup-destination" {
		permission = "backup.manage"
	}
	if kind == "acme-order" {
		permission = "certificate.read"
	}
	u, err := a.require(r, permission)
	if err != nil {
		return err
	}
	v, err := a.system.Configuration(r.Context(), 0)
	if err != nil {
		return err
	}
	var raw []byte
	var last *configstate.Task
	err = a.db.Row(r.Context(), `SELECT payload FROM configuration_checks WHERE feature=$1 ORDER BY (node_id=$2) DESC,started_at DESC,id DESC LIMIT 1`, f.ID, a.configurationNode).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	actor := u.ID
	if err == nil {
		var t configstate.Task
		if json.Unmarshal(raw, &t) != nil {
			return fault.Invalid
		}
		t = configstate.Normalize(t, time.Now())
		last = &t

	}
	evidence, enabled, err := a.dependencyEvidence(r.Context(), v.Values, f.ID, actor)
	if err != nil {
		return err
	}
	state := configstate.Dependency(last, a.configurationNode, v.ID, nil, time.Now())
	if last != nil {
		previous, _, e := a.dependencyEvidence(r.Context(), v.Values, f.ID, last.Actor)
		if e != nil || last.Evidence != previous {
			state = "stale"
		}
	}
	desired := "disabled"
	if enabled {
		desired = "enabled"
	}
	item := configurationFeature{Feature: f, CheckSupported: true, LastCheck: last, Desired: configurationDimension{State: desired, Scope: kind}, Applied: configurationDimension{State: "applied", Scope: "resource-storage", Reason: "资源配置已保存；不表示连接或实际业务成功"}, Dependencies: configurationDimension{State: state, Scope: "node-resource", Reason: "证据绑定资源版本、配置版本和当前节点；业务验证仍须显式执行"}, Service: configurationDimension{State: "unknown", Scope: "business-verification", Reason: "预检不代表实际投递、登录或模型调用成功"}}
	if kind == "backup-destination" && last != nil && last.Mode == "verify" && state == "ready" {
		item.Service = configurationDimension{State: "running", Scope: "isolated-backup-roundtrip", Reason: "当前证据已验证隔离对象上传、摘要回读与清理；不代表业务备份已完成"}
	}
	if kind == "acme-order" {
		var status string
		if a.db.Row(r.Context(), `SELECT status FROM certificate_orders WHERE id=$1`, id).Scan(&status) == nil {
			item.Service.Reason = "实际签发结果以订单状态和日志为准：" + status
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"version": 1, "revision": v.ID, "node": a.configurationNode, "checkedAt": time.Now().UTC(), "evidence": evidence, "items": []configurationFeature{item}})
	return nil
}
