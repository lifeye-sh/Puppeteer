package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"puppeteer/backend/internal/modules/backup"
	"puppeteer/backend/internal/modules/identity"
	"puppeteer/backend/internal/modules/notification"
	"puppeteer/backend/internal/modules/system"
	"puppeteer/backend/internal/platform/configstate"
	"puppeteer/backend/internal/platform/secretbox"
)

func externalCheckSupported(id string) bool {
	switch id {
	case "mail", "ldap", "oidcProvider", "wecomProvider", "passkey", "aiModel", "oauthServer", "native-client", "forward", "databasePolicy":
		return true
	}
	_, target := dependencyResource(id)
	return target != ""
}

func (a *App) externalCheckEvidence(v system.ConfigurationValues) string {
	// Include deployment fallback identities as well as the persisted revision.
	// Values contain opaque secret references, never decrypted credentials.
	raw, _ := json.Marshal([]any{v, a.config.PasskeyRPID, a.config.PasskeyOrigins, a.config.AllowedOrigins})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (a *App) dependencySecret(ctx context.Context, v system.ConfigurationValues, key string) ([]byte, error) {
	ref := v.String(key)
	if ref == "" {
		return nil, nil
	}
	var sealed []byte
	if err := a.db.Row(ctx, `SELECT sealed FROM configuration_secrets WHERE id=$1 AND key=$2`, ref, key).Scan(&sealed); err != nil {
		return nil, err
	}
	return secretbox.Open(a.config.VaultKey, sealed, "settings:"+key+":"+ref)
}

func (a *App) runExternalDependencyCheck(ctx context.Context, t configstate.Task, v system.ConfigurationValues) {
	initial, _, evidenceErr := a.dependencyEvidence(ctx, v, t.Feature, t.Actor)
	if t.Evidence != "" && t.Evidence != initial {
		evidenceErr = errors.New("resource_changed")
	}
	t.Evidence = initial
	t.Stage = "dependency"
	report := func(c configstate.Check) bool {
		if t.Mode == "verify" {
			t.Stage = c.Code
		}
		t.Checks = append(t.Checks, c)
		t.Updated = time.Now().UTC()
		return ctx.Err() == nil && a.storeConfigurationCheck(ctx, t)
	}
	check := func(code string, err error, success, failure, remedy string) bool {
		state, summary := "ready", success
		if err != nil {
			state, summary = "blocked", failure
		}
		return report(configstate.Check{Code: code, State: state, Summary: summary, Remediation: remedy, Scope: "external-dependency"}) && err == nil
	}
	if !a.storeConfigurationCheck(ctx, t) {
		return
	}
	feature := t.Feature
	kind, target := dependencyResource(feature)
	if target != "" {
		feature = map[string]string{"notification-channel": "channel", "ldap-directory": "ldap", "oidc-provider": "oidcProvider", "ai-profile": "aiModel", "backup-destination": "backup-destination", "acme-order": "acme-order"}[kind]
		if kind == "agent-gateway" || kind == "ssh-jump" || kind == "gateway-group" || kind == "network-proxy" {
			feature = "connectivity"
		}
	}
	if !check("dependency_snapshot", evidenceErr, "已锁定检测配置版本", "资源已变化、删除或无法读取", "刷新资源状态后重新预检") {
		feature = ""
	}
	switch feature {
	case "connectivity":
		a.connectivityDependencyCheck(ctx, t, kind, target, report)
	case "oauthServer", "native-client", "forward", "databasePolicy":
		a.checkAccessDependencies(ctx, t, v, report)
	case "backup-destination":
		d, err := backup.New(a.db).DestinationSecret(ctx, target, a.config.VaultKey)
		d.ID = target
		if check("backup_configuration", err, "备份目标与凭据可读取", "备份目标或凭据无法读取", "检查目标配置与密钥存储") && check("backup_runtime", func() error {
			if a.config.BackupDirectory == "" {
				return errors.New("unavailable")
			}
			return nil
		}(), "当前节点配置了备份运行目录", "当前节点尚未配置备份运行目录", "在备份部署中配置运行目录") {
			if t.Mode == "verify" {
				current, _, e := a.dependencyEvidence(ctx, v, t.Feature, t.Actor)
				if e == nil && current != t.Evidence {
					e = errors.New("changed")
				}
				if check("backup_version", e, "写入前资源版本复核通过", "写入前配置已变化", "刷新后重新验证") {
					a.verifyBackupDependency(ctx, t, d, report)
				}
			} else {
				check("backup_connection", backup.ProbeRemote(ctx, d), "远端连接、身份验证及目标路径只读检查通过；读写、摘要和删除权限须显式验证", "远端路径、认证或 TLS/主机指纹检查失败", "检查目标地址、既有路径、凭据、CA/主机指纹；本预检不创建目录")
			}
		}
	case "acme-order":
		a.certificates().PreflightOrder(ctx, target, report)
	case "channel":
		a.notifications().Preflight(ctx, target, report)
	case "aiModel":
		id := target
		var err error
		if id == "" {
			err = a.db.Row(ctx, `SELECT id FROM ai_profiles WHERE settings_managed AND deleted_at IS NULL`).Scan(&id)
		}
		if check("ai_registered", err, "已找到模型配置", "尚未保存平台默认模型", "先保存外部依赖中的模型配置") {
			a.aiService().Preflight(ctx, id, t.Actor, report)
		}
	case "mail":
		secret, err := a.dependencySecret(ctx, v, "mail-password")
		defer clear(secret)
		if check("smtp_secret", err, "SMTP 凭据读取完成", "SMTP 凭据无法读取", "检查密钥存储，重新保存 SMTP 密码后重试") {
			notification.ProbeSMTP(ctx, notification.Config{Host: v.String("mail-host"), Port: v.Int("mail-port"), ImplicitTLS: v.Bool("mail-use-ssl"), Username: v.String("mail-username"), From: v.String("mail-from"), CA: v.String("mail-ca-pem")}, string(secret), report)
		}
	case "ldap":
		id := target
		var err error
		if id == "" {
			err = a.db.Row(ctx, `SELECT id FROM ldap_identity_providers WHERE settings_managed`).Scan(&id)
		}
		if check("ldap_registered", err, "已找到 LDAP 目录", "尚未保存默认 LDAP 目录", "先保存目录地址、服务账号和身份映射") {
			p, err := a.identity.LDAPProvider(ctx, id)
			if err == nil {
				err = identity.ValidateLDAPProvider(p)
			}
			if check("ldap_configuration", err, "LDAP TLS、查询范围和身份映射配置有效", "LDAP 配置或身份映射无效", "检查 CA、Base DN、过滤器及不可变身份属性") {
				secret, err := a.identity.LDAPPassword(ctx, id, a.config.VaultKey)
				if err == nil {
					_, err = identity.VerifyLDAP(ctx, p, secret, "", "")
				}
				if check("ldap_service_bind", err, "LDAP TLS、服务绑定和 Base DN 可见性通过", "LDAP TLS、服务绑定或 Base DN 检查失败", "检查证书、服务账号密码及目录读取权限") {
					entries, err := identity.ReadLDAPDirectory(ctx, p, secret, p.DirectoryLimit())
					if err == nil && len(entries) == 0 {
						err = errors.New("empty_directory")
					}
					check("ldap_mapping", err, "目录查询与返回记录的唯一身份及属性映射通过；未同步或创建用户", "目录查询为空、超出上限或身份映射验证失败", "检查搜索范围、读取上限和身份属性；用户密码登录须在目录页面单独验证")
				}
			}
		}
	case "oidcProvider", "wecomProvider":
		kind := "oidc"
		if t.Feature == "wecomProvider" {
			kind = "wecom"
		}
		id := target
		var err error
		if id == "" {
			err = a.db.Row(ctx, `SELECT id FROM oidc_identity_providers WHERE settings_managed AND kind=$1`, kind).Scan(&id)
		}
		if check("identity_registered", err, "已找到身份源", "尚未保存默认身份源", "先保存外部依赖中的身份源配置") {
			p, err := a.identity.OIDCProvider(ctx, id)
			if err == nil {
				err = identity.ValidateOIDCProvider(p)
			}
			if check("identity_configuration", err, "身份源参数及回调地址格式通过", "身份源参数或回调地址不完整", "检查 Issuer、客户端 ID、回调及权限范围") {
				secret, err := a.identity.OIDCClientSecret(ctx, id, a.config.VaultKey)
				if err == nil && secret == "" {
					err = errors.New("missing_secret")
				}
				if check("identity_secret", err, "身份源凭据可读取；尚未验证授权回调", "身份源凭据缺失或无法解密", "保存匹配的客户端或应用密钥") {
					if p.Kind != "wecom" {
						_, err = identity.DiscoverOIDC(ctx, p.Issuer, a.oidcHTTPClient)
						check("oidc_discovery", err, "OIDC 发现文档与协议能力通过；客户端认证、回调和用户身份映射待验证授权", "OIDC 发现文档、TLS 或协议能力验证失败", "检查 HTTPS、Issuer 精确匹配及 code / RS256 / S256；到身份源页面验证授权")
					} else {
						protocol, e := identity.NewWeComProtocol(p, secret, a.oidcHTTPClient)
						if e == nil {
							e = protocol.Check(ctx)
						}
						check("wecom_credentials", e, "企业微信应用凭据验证通过；可信域名、用户授权及映射待验证授权", "企业微信应用凭据或 TLS 检查失败", "检查企业 ID、应用 Secret 与证书；在提供方配置回调后验证授权")
					}
				}
			}
		}
	case "passkey":
		_, err := a.configuredPasskey(v)
		check("passkey_configuration", err, "Passkey HTTPS 来源、RP ID 与 API Origin 白名单检查通过；浏览器及实际注册须另行验证", "Passkey 来源、RP ID 或部署 Origin 白名单不符合要求", "配置有效的 HTTPS 来源与 RP ID，并将来源加入部署 API Origin 白名单")
	}
	latest, err := a.system.Configuration(ctx, 0)
	current, _, currentErr := a.dependencyEvidence(ctx, latest.Values, t.Feature, t.Actor)
	if err != nil || currentErr != nil || latest.ID != t.Revision || current != t.Evidence {
		check("dependency_inputs_changed", errors.New("changed"), "", "检测期间配置已变化，结果不可用于当前配置", "刷新配置并重新预检")
	}
	t.State = "succeeded"
	for _, c := range t.Checks {
		if c.State != "ready" {
			t.State = "failed"
		}
	}
	if len(t.Checks) == 0 || ctx.Err() != nil {
		t.State = "failed"
	}
	t.Stage = "complete"
	t.Updated = time.Now().UTC()
	t.Expires = t.Updated.Add(5 * time.Minute)
	a.storeConfigurationCheck(ctx, t)
}
