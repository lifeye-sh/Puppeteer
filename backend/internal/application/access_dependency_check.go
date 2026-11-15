package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"time"

	"puppeteer/backend/internal/modules/database"
	"puppeteer/backend/internal/modules/identity"
	"puppeteer/backend/internal/modules/system"
	"puppeteer/backend/internal/platform/configstate"
	"puppeteer/backend/internal/platform/mysqlproxy"
)

func (a *App) checkAccessDependencies(ctx context.Context, t configstate.Task, v system.ConfigurationValues, report func(configstate.Check) bool) {
	check := func(code string, ok bool, good, bad, remedy string) bool {
		state, summary := "ready", good
		if !ok {
			state, summary = "blocked", bad
		}
		return report(configstate.Check{Code: code, State: state, Summary: summary, Remediation: remedy, Scope: "access-dependency", CheckedAt: time.Now().UTC()}) && ok
	}
	listening := func(name string) bool {
		a.configurationMu.Lock()
		defer a.configurationMu.Unlock()
		if a.listenerManager != nil {
			for _, s := range a.listenerManager.States() {
				if s.Name == name {
					return true
				}
			}
		}
		return false
	}
	if t.Feature == "oauthServer" || t.Feature == "native-client" {
		s, err := a.identity.OAuthSettings(ctx)
		if !check("oauth_issuer", err == nil && s.Issuer != "", "固定 Issuer 配置已读取", "OIDC Issuer 未配置或无法读取", "配置固定的 HTTPS 公开来源") {
			return
		}
		public, err := a.identity.OAuthPreflightKey(ctx)
		if !check("oauth_keys", err == nil, "签名与 HMAC 密钥可解密且签名公私钥匹配", "签名或 HMAC 密钥缺失、无法解密或不匹配", "在 OAuth 密钥页面配置或生成密钥；预检不会生成密钥") {
			return
		}
		metadata, err := identity.DiscoverOIDC(ctx, s.Issuer, a.oidcHTTPClient)
		if !check("oauth_discovery", err == nil && metadata.JWKSURI == s.Issuer+"/api/oidc/server/jwks", "公开 HTTPS 发现文档和 Issuer 验证通过", "公开发现文档不可用或与当前平台配置不一致", "检查公开入口、HTTPS 证书及配置发布状态") {
			return
		}
		var keys struct {
			Keys []json.RawMessage `json:"keys"`
		}
		err = dependencyGetJSON(ctx, a.oidcHTTPClient, metadata.JWKSURI, &keys)
		var expected map[string]any
		_ = json.Unmarshal(public, &expected)
		found := false
		for _, raw := range keys.Keys {
			var key map[string]any
			if json.Unmarshal(raw, &key) == nil && key["kid"] == expected["kid"] && key["n"] == expected["n"] && key["e"] == expected["e"] && key["kty"] == "RSA" {
				found = true
			}
		}
		if !check("oauth_jwks", err == nil && found, "公开 JWKS 包含当前签名公钥", "公开 JWKS 与当前节点密钥不一致或不可读取", "检查反向代理、节点密钥一致性和缓存") {
			return
		}
		if t.Feature == "native-client" {
			client, err := a.identity.OAuthClient(ctx, identity.NativeClientUUID, false)
			allowed := false
			if err == nil {
				allowed, err = a.identity.OAuthUserAllowed(ctx, client, t.Actor)
			}
			if !check("native_client_registration", err == nil && s.NativeEnabled && client.Enabled && slices.Contains(client.Scopes, "sessions:connect") && allowed, "原生客户端已登记且允许发起人使用；资产权限仍逐次检查", "原生客户端未启用、缺少范围或发起人无权使用", "检查客户端登记、配置发布及用户授权") {
				return
			}
			check("native_protocol_entry", listening("ssh") || listening("rdp"), "当前节点至少有一个 SSH/RDP 原生入口监听；具体入口及完整会话须单独验证", "当前节点没有 SSH/RDP 原生协议入口", "在服务部署中启用所需入口；不会因客户端开关自动开放全部协议")
		}
		return
	}
	if t.Feature == "forward" {
		settings, err := a.forwardSettings(ctx)
		if !check("forward_settings", err == nil, "受控转发配置已读取", "无法读取受控转发配置", "恢复配置存储后重试") {
			return
		}
		if !v.Bool(system.ForwardEnabledKey) {
			clear := a.waitDependencyConnections(ctx, `SELECT count(*) FROM native_forward_streams WHERE ended_at IS NULL`)
			check("forward_revoked", !settings.Enabled && clear, "受控转发已关闭且没有未结束的转发记录；共享 SSH 入口保留", "仍有转发记录未结束或停用策略尚未生效", "等待在途连接撤销并检查会话记录；不删除共享 SSH 映射")
			return
		}
		if !check("forward_gateway", settings.Enabled && listening("ssh") && len(settings.Rules) > 0, "受控转发策略与 SSH 监听已生效", "受控转发策略、规则或 SSH 监听未就绪", "检查配置发布、SSH 网关和精确目标规则") {
			return
		}
		for i, rule := range settings.Rules {
			var exists bool
			err = a.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM assets a JOIN resource_registry r ON r.id=a.id WHERE a.id=$1 AND a.protocol='ssh' AND r.deleted_at IS NULL)`, rule.AssetID).Scan(&exists)
			ok := err == nil && exists
			if ok {
				ok = false
				c, cancel := context.WithTimeout(ctx, 3*time.Second)
				for _, ip := range rule.Addresses {
					if !system.ForwardAddress(ip) {
						continue
					}
					conn, e := (&net.Dialer{}).DialContext(c, "tcp", net.JoinHostPort(ip, strconv.Itoa(rule.Port)))
					if e == nil {
						conn.Close()
						ok = true
						break
					}
				}
				cancel()
			}
			if !check(fmt.Sprintf("forward_target_%d", i+1), ok, fmt.Sprintf("规则 %d 的资产引用和已固定目标 TCP 可达；未授予会话权限", i+1), fmt.Sprintf("规则 %d 的资产引用或固定目标连接失败", i+1), "检查规则对应资产和目标网络；实际转发还需用户权限、MFA 与会话策略") {
				return
			}
		}
		return
	}
	if t.Feature == "databasePolicy" {
		security, err := a.system.SecurityConfiguration(ctx)
		if !check("dml_policy", err == nil, "数据库写入策略已读取", "数据库写入策略无法读取", "恢复配置存储后重试") {
			return
		}
		if v.Bool(system.DatabaseBlockDMLKey) {
			clear := a.waitDependencyConnections(ctx, `SELECT count(*) FROM database_sessions WHERE access_profile='dml' AND ended_at IS NULL`)
			check("dml_revoked", security.Values.Bool(system.DatabaseBlockDMLKey) && clear, "原生 DML 已禁止且没有未结束的写会话记录；共享代理保留", "写会话记录仍未结束或禁止策略尚未生效", "等待租约撤销并查看 SQL 日志；未知事务结果须在目标库核验")
			return
		}
		if !check("dml_proxy", !security.Values.Bool(system.DatabaseBlockDMLKey) && listening("mysql"), "MySQL 写入策略与代理监听已生效；PostgreSQL 仍为只读", "MySQL 代理或写入策略未生效", "检查 MySQL 部署及配置发布，不能把此项用于 PostgreSQL 写入") {
			return
		}
		assets, err := database.New(a.db).List(ctx, true, nil)
		if !check("dml_assets", err == nil, "已读取数据库资产", "数据库资产无法读取", "检查资产存储") {
			return
		}
		count := 0
		for _, asset := range assets {
			if !asset.Enabled || asset.Engine != "mysql" || asset.NativeWriteCredentialID == "" {
				continue
			}
			count++
			var target mysqlproxy.Target
			err = a.db.Within(ctx, func(c context.Context) error {
				credential, secret, e := a.vault.DatabaseAuth(c, asset.NativeWriteCredentialID, t.Actor, "configuration-dml-preflight")
				if e != nil {
					return e
				}
				target = mysqlproxy.Target{ProgramPermissions: asset.PermissionMode == "program", Address: net.JoinHostPort(asset.Host, strconv.Itoa(asset.Port)), User: credential.Username, Password: secret.Password, Database: asset.Database, ServerName: asset.TLSServerName, CA: asset.TLSCA, TLSMode: asset.TLSMode, Timeout: time.Duration(asset.ConnectTimeout) * time.Second}
				return nil
			})
			if err == nil {
				conn, e := mysqlproxy.ConnectDMLTarget(ctx, target)
				err = e
				if conn != nil {
					conn.Close()
				}
			}
			if !check(fmt.Sprintf("dml_asset_%d", count), err == nil, fmt.Sprintf("DML 资产 %d 的独立凭据连接与对应权限模式检查通过；未执行写 SQL", count), fmt.Sprintf("DML 资产 %d 的独立凭据或目标策略检查失败", count), "到数据库资产页面查看凭据诊断；实际用户仍须 database.write 和资产授权") {
				return
			}
		}
		check("dml_credentials", count > 0, "已配置可检查的 MySQL 原生 DML 资产；不扩大任何用户权限", "没有启用且配置独立 DML 凭据的 MySQL 资产", "在数据库资产中配置可选的原生 DML 凭据")
	}
}

func (a *App) waitDependencyConnections(ctx context.Context, query string) bool {
	c, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		if a.db.Row(c, query).Scan(&count) != nil {
			return false
		}
		if count == 0 {
			return true
		}
		select {
		case <-c.Done():
			return false
		case <-ticker.C:
		}
	}
}

func dependencyGetJSON(ctx context.Context, client *http.Client, endpoint string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	copy := *client
	copy.Jar = nil
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return err
	}
	res, err := copy.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("dependency_unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 256*1024+1))
	if err != nil || len(raw) > 256*1024 {
		return errors.New("invalid_response")
	}
	return json.Unmarshal(raw, out)
}

// Hash dependencies that may change without a platform settings revision.
// Secret bytes are never read here; credential/key identities and versions suffice.
func (a *App) accessDependencyEvidence(ctx context.Context, feature, actor string) (any, error) {
	if feature != "oauthServer" && feature != "native-client" && feature != "forward" && feature != "databasePolicy" {
		return nil, nil
	}
	var state string
	query := `SELECT jsonb_build_array(
 (SELECT version FROM users WHERE id=$1),
 (SELECT coalesce(jsonb_agg(department_id ORDER BY department_id),'[]') FROM user_departments WHERE user_id=$1),
 (SELECT coalesce(jsonb_agg(jsonb_build_array(id,version,enabled,deleted_at) ORDER BY id),'[]') FROM resource_registry),
 (SELECT coalesce(jsonb_agg(jsonb_build_array(id,version,active_version,deleted_at) ORDER BY id),'[]') FROM credentials),
 (SELECT coalesce(jsonb_agg(jsonb_build_array(id,version) ORDER BY id),'[]') FROM oauth_clients),
 (SELECT coalesce(jsonb_agg(id ORDER BY id),'[]') FROM oauth_signing_keys WHERE active),
 (SELECT coalesce(jsonb_agg(id ORDER BY id),'[]') FROM oauth_hmac_keys WHERE active),
 (SELECT version FROM oauth_server_settings WHERE singleton=1),
 (SELECT version FROM native_forward_settings WHERE singleton=1)
 )::text`
	if err := a.db.Row(ctx, query, actor).Scan(&state); err != nil {
		return nil, err
	}
	listeners := []string{}
	a.configurationMu.Lock()
	if a.listenerManager != nil {
		for _, s := range a.listenerManager.States() {
			listeners = append(listeners, s.Name)
		}
	}
	a.configurationMu.Unlock()
	slices.Sort(listeners)
	return []any{state, listeners}, nil
}
