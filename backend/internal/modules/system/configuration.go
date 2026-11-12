package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"next-terminal/backend/internal/platform/fault"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Property struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Type    string `json:"type"`
	Effect  string `json:"effect"`
	Default any    `json:"default"`
	Min     int    `json:"min"`
	Max     int    `json:"max"`
	Unit    string `json:"unit"`
}

func PropertySchema() []Property {
	return []Property{
		{LoginCaptchaKey, "登录拼图验证码", "boolean", "security-live", true, 0, 0, ""},
		{"native-rdp-enabled", "原生 RDP 监听", "boolean", "listener", true, 0, 0, ""},
		{"native-rdp-addr", "原生 RDP 监听地址", "string", "listener", "", 0, 260, ""},
		{"native-rdp-public-address", "原生 RDP 客户端地址", "string", "listener", "", 0, 260, ""},
		{"http-server-addr", "管理 HTTP 监听地址", "string", "listener", "", 0, 260, ""},
		{"website-server-enabled", "网站 TLS 监听", "boolean", "listener", true, 0, 0, ""},
		{"website-server-addr", "网站 TLS 监听地址", "string", "listener", "", 0, 260, ""},
		{DatabaseBlockDMLKey, "阻止数据库原生 DML", "boolean", "security-live", true, 0, 0, ""},
		{NativePasswordDisabledKey, "原生 SSH 禁用密码认证", "boolean", "security-live", true, 0, 0, ""},
		{"access-require-mfa", "远程资产接入二次验证", "boolean", "security-live", false, 0, 0, ""},
		{"access-mfa-expires-at", "接入验证复用时间", "integer", "security-live", 5, 0, 1440, "分钟"},
		{"oidc-department", "OIDC 预建用户默认部门", "string", "new-session", "", 0, 36, ""},
		{"wechat-work-department", "企业微信预建用户默认部门", "string", "new-session", "", 0, 36, ""},
		{OAuthSigningKey, "OIDC RS256 私钥", "secret", "new-session", "", 0, 8192, ""},
		{OAuthHMACKey, "OIDC HMAC 密钥", "secret", "new-session", "", 0, 43, ""},
		{OAuthServerEnabledKey, "OIDC 服务端", "boolean", "security-live", false, 0, 0, ""},
		{OAuthServerIssuerKey, "固定 OIDC Issuer", "string", "new-session", "", 0, 512, ""},
		{NativeClientEnabledKey, "原生客户端访问", "boolean", "security-live", false, 0, 0, ""},
		{"session-saved-limit-days", "会话内容保留期限", "integer", "next-job", 31, -1, 3650, "天"},
		{"recording-enabled", "保存新会话录屏", "boolean", "security-live", true, 0, 0, ""},
		{ForwardEnabledKey, "SSH 受控端口转发", "boolean", "security-live", false, 0, 0, ""},
		{ForwardRulesKey, "SSH 转发目标规则", "string", "security-live", "[]", 2, 65536, ""},
		{"oidc-enabled", "平台 OIDC 登录", "boolean", "security-live", false, 0, 0, ""},
		{"oidc-binding-enabled", "允许绑定平台 OIDC", "boolean", "security-live", false, 0, 0, ""},
		{"oidc-issuer", "OIDC Issuer", "string", "new-session", "", 0, 2048, ""},
		{"oidc-client-id", "OIDC 客户端 ID", "string", "new-session", "", 0, 512, ""},
		{"oidc-client-secret", "OIDC 客户端密钥", "secret", "new-session", "", 0, 8192, ""},
		{"oidc-redirect-uri", "OIDC 回调地址", "string", "new-session", "", 0, 2048, ""},
		{"oidc-scopes", "OIDC 权限范围", "string", "new-session", "openid", 1, 4096, ""},
		{"wechat-work-enabled", "平台企业微信登录", "boolean", "security-live", false, 0, 0, ""},
		{"wechat-work-binding-enabled", "允许绑定平台企业微信", "boolean", "security-live", false, 0, 0, ""},
		{"wechat-work-corp-id", "企业微信 Corp ID", "string", "new-session", "", 0, 128, ""},
		{"wechat-work-agent-id", "企业微信 Agent ID", "string", "new-session", "", 0, 32, ""},
		{"wechat-work-secret", "企业微信应用密钥", "secret", "new-session", "", 0, 8192, ""},
		{"wechat-work-redirect-uri", "企业微信回调地址", "string", "new-session", "", 0, 2048, ""},
		{"wechat-work-api-url", "企业微信 API 地址", "string", "new-session", "https://qyapi.weixin.qq.com", 1, 2048, ""},
		{"wechat-work-authorization-url", "企业微信授权地址", "string", "new-session", "https://login.work.weixin.qq.com/wwlogin/sso/login", 1, 2048, ""},
		{"oidc-server-access-token-ttl", "访问令牌有效期", "integer", "new-session", 300, 60, 3600, "秒"},
		{"oidc-server-refresh-token-ttl", "刷新令牌家族有效期", "integer", "new-session", 2592000, 3600, 2592000, "秒"},
		{"oidc-server-id-token-ttl", "ID Token 有效期", "integer", "new-session", 300, 60, 3600, "秒"},
		{"oidc-server-auth-code-ttl", "授权码有效期", "integer", "new-session", 60, 30, 300, "秒"},
		{"passkey-enabled", "允许 Passkey 登录与注册", "boolean", "security-live", true, 0, 0, ""},
		{"passkey-domain", "Passkey RP ID", "string", "new-session", "", 0, 253, ""},
		{"passkey-origins", "Passkey HTTPS 来源", "string", "new-session", "[]", 2, 32768, ""},
		{"access-max-idle-second", "远程会话空闲超时", "integer", "security-live", 900, -1, 86400, "秒"},
		{"ldap-enabled", "平台 LDAP 登录", "boolean", "security-live", false, 0, 0, ""},
		{"ldap-binding-enabled", "允许绑定平台 LDAP", "boolean", "security-live", false, 0, 0, ""},
		{"ldap-url", "LDAP 目录地址", "string", "new-session", "", 0, 2048, ""},
		{"ldap-user", "LDAP 服务账号 DN", "string", "new-session", "", 0, 1024, ""},
		{"ldap-password", "LDAP 服务账号密码", "secret", "new-session", "", 0, 65536, ""},
		{"ldap-base-dn", "LDAP Base DN", "string", "new-session", "", 0, 1024, ""},
		{"ldap-user-search-size-limit", "LDAP 同步读取上限", "integer", "next-job", 1000, 1, 10000, "个"},
		{"ldap-user-search-filter", "LDAP 用户范围过滤器", "string", "new-session", "(objectClass=inetOrgPerson)", 1, 4096, ""},
		{"ldap-user-property-mapping", "LDAP 属性映射", "string", "new-session", DefaultLDAPMapping, 2, 1024, ""},
		{"ldap-identity-attribute", "LDAP 稳定身份属性", "string", "new-session", "entryUUID", 1, 64, ""},
		{"ldap-ca-pem", "LDAP 内部 CA 证书", "string", "new-session", "", 0, 65536, ""},
		{"agent-gateway-endpoint", "Agent 默认后端地址", "string", "new-session", "", 0, 2048, ""},
		{"llm-profile-enabled", "启用平台默认模型", "boolean", "next-job", false, 0, 0, ""},
		{"llm-base-url", "模型 API 根地址", "string", "next-job", "", 0, 2048, ""},
		{"llm-model", "模型标识", "string", "next-job", "", 0, 200, ""},
		{"llm-api-key", "模型 API Key", "secret", "next-job", "", 0, 4096, ""},
		{"llm-temperature", "Temperature", "number", "next-job", float64(-1), -1, 2, ""},
		{"llm-max-tokens", "最大输出 Token", "integer", "next-job", 1024, 64, 4096, ""},
		{"llm-proxy-url", "模型网络代理", "string", "next-job", "", 0, 2048, ""},
		{"llm-allow-private", "允许可信内网模型", "boolean", "next-job", false, 0, 0, ""},
		{"llm-protocol", "模型接口协议", "string", "next-job", "openai", 1, 16, ""},
		{"llm-input-rate", "输入价格", "integer", "next-job", 0, 0, 1000000000, "微美元/千Token"},
		{"llm-output-rate", "输出价格", "integer", "next-job", 0, 0, 1000000000, "微美元/千Token"},
		{"llm-enabled", "AI 助手", "boolean", "next-job", false, 0, 0, ""},
		{"llm-shell-enabled", "AI 终端工具", "boolean", "next-job", false, 0, 0, ""},
		{"llm-audit-enabled", "AI 命令审计分析", "boolean", "next-job", false, 0, 0, ""},
		{"llm-shell-prompt", "终端工具补充提示词", "string", "next-job", "", 0, 32768, ""},
		{"llm-audit-prompt", "审计分析补充提示词", "string", "next-job", "", 0, 32768, ""},
		{"disable-password-login", "关闭本地密码登录", "boolean", "security-live", false, 0, 0, ""},
		{"ssh-server-private-key", "SSH 网关主机私钥", "secret", "listener", "", 0, 8192, ""},
		{"ip-extractor", "客户端 IP 提取方式", "string", "security-live", "direct", 1, 64, ""},
		{"ip-trust-list", "可信反向代理网段", "string", "security-live", "", 0, 65536, ""},
		{"mail-enabled", "平台邮件渠道", "boolean", "next-job", false, 0, 0, ""},
		{"mail-use-ssl", "使用隐式 TLS", "boolean", "next-job", true, 0, 0, ""},
		{"mail-host", "SMTP 主机", "string", "next-job", "", 0, 253, ""},
		{"mail-port", "SMTP 端口", "integer", "next-job", 465, 1, 65535, ""},
		{"mail-username", "SMTP 用户名", "string", "next-job", "", 0, 254, ""},
		{"mail-password", "SMTP 密码", "secret", "next-job", "", 0, 8192, ""},
		{"mail-insecure-skip-verify", "跳过 TLS 验证", "boolean", "next-job", false, 0, 0, ""},
		{"mail-from", "发件人邮箱", "string", "next-job", "", 0, 254, ""},
		{"mail-ca-pem", "内网 CA 证书", "string", "next-job", "", 0, 65536, ""},
		{"ssh-server-enabled", "SSH 网关监听", "boolean", "listener", true, 0, 0, ""},
		{"ssh-server-addr", "SSH 监听地址", "string", "listener", "", 0, 260, ""},
		{"db-proxy-enabled", "MySQL 代理监听", "boolean", "listener", true, 0, 0, ""},
		{"db-proxy-addr", "MySQL 监听地址", "string", "listener", "", 0, 260, ""},
		{"postgres-proxy-enabled", "PostgreSQL 代理监听", "boolean", "listener", true, 0, 0, ""},
		{"postgres-proxy-addr", "PostgreSQL 监听地址", "string", "listener", "", 0, 260, ""},
		{"db-proxy-certificate-id", "数据库代理证书", "string", "listener", "", 0, 36, ""},
		{"login-force-totp-enabled", "强制登录 TOTP 验证", "boolean", "security-live", false, 0, 0, ""},
		{"password-expiration-period", "本地密码有效期", "integer", "security-live", -1, -1, 3650, "天"},
		{"password-strength-type", "密码强度策略", "string", "security-live", "recommend", 9, 9, ""},
		{"password-strength-policy", "自定义密码规则", "string", "security-live", DefaultPasswordPolicy, 2, 1024, ""},
		{"watermark-enabled", "显示会话水印", "boolean", "new-session", true, 0, 0, ""},
		{"watermark-content", "水印文字", "string", "new-session", "受控访问", 0, 256, ""},
		{"watermark-content-asset-username", "附加资产账户", "boolean", "new-session", false, 0, 0, ""},
		{"watermark-content-user-account", "附加平台用户", "boolean", "new-session", true, 0, 0, ""},
		{"watermark-font-color", "水印颜色", "string", "new-session", "rgba(128, 128, 128, 0.3)", 7, 40, ""},
		{"watermark-font-size", "水印字号", "integer", "new-session", 16, 1, 100, "px"},
		{"user-default-storage-size", "新用户默认存储容量", "integer", "new-session", 5368709120, 0, 1099511627776, "Byte"},
		{"user-client-cert-valid-days", "客户端证书默认有效期", "integer", "new-session", 90, 1, 825, "天"},
		{"access-log-saved-limit-days", "访问记录保留", "integer", "next-job", 31, -1, 3650, "天"},
		{"login-log-saved-limit-days", "登录日志保留", "integer", "next-job", -1, -1, 3650, "天"},
		{"cron-log-saved-limit-days", "任务日志保留", "integer", "next-job", -1, -1, 3650, "天"},
		{"db-sql-log-saved-limit-days", "SQL 日志保留", "integer", "next-job", -1, -1, 3650, "天"},
		{"operation-log-saved-limit-days", "操作日志保留", "integer", "next-job", -1, -1, 3650, "天"},
		{"filesystem-log-saved-limit-days", "文件操作日志保留", "integer", "next-job", -1, -1, 3650, "天"},
		{"system-event-saved-limit-days", "系统事件保留", "integer", "next-job", -1, -1, 3650, "天"},
		{"system-name", "系统名称", "string", "live", "Puppeteer", 1, 64, ""},
		{"system-copyright", "页脚说明", "string", "live", "运维管理平台", 0, 160, ""},
		{"system-icp", "备案信息", "string", "live", "", 0, 64, ""},
		{"system-logo", "系统 Logo", "string", "live", "", 0, 36, ""},
		{"login-lock-enabled", "登录失败锁定", "boolean", "security-live", true, 0, 0, ""},
		{"login-lock-ip-duration", "来源 IP 锁定时长", "integer", "security-live", 0, 0, 10080, "分钟"},
		{"login-session-browser-close-logout", "使用浏览器会话 Cookie", "boolean", "new-session", false, 0, 0, ""},
		{"login-session-count-custom", "限制每人登录会话数", "boolean", "new-session", false, 0, 0, ""},
		{"login-session-count-limit", "每人登录会话上限", "integer", "new-session", 5, 1, 1000, "个"},
		{"login-lock-failed-duration", "失败统计时长", "integer", "security-live", 15, 1, 1440, "分钟"},
		{"login-lock-failed-times", "失败次数阈值", "integer", "security-live", 5, 1, 100, "次"},
		{"login-lock-account-duration", "账户锁定时长", "integer", "security-live", 15, 0, 10080, "分钟"},
		{"login-session-duration", "登录会话有效时长", "integer", "new-session", 480, 1, 525600, "分钟"},
		{"postgres-query-timeout-seconds", "查询超时", "integer", "new-session", 10, 1, 20, "秒"},
		{"postgres-result-max-rows", "返回行数上限", "integer", "new-session", 100000, 1, 100000, "行"},
		{"postgres-result-max-mib", "响应大小上限", "integer", "new-session", 64, 1, 64, "MiB"},
	}
}

type Revision struct {
	Secrets     map[string]SecretStatus `json:"secrets,omitempty"`
	ID          int64                   `json:"id"`
	Values      ConfigurationValues     `json:"values"`
	ChangedKeys []string                `json:"changedKeys"`
	Actor       string                  `json:"actor"`
	Legacy      bool                    `json:"legacyWrite"`
	CreatedAt   time.Time               `json:"createdAt"`
}
type ConfigNode struct {
	ID                  string     `json:"id"`
	LastAppliedRevision int64      `json:"lastAppliedRevision"`
	State               string     `json:"state"`
	Error               string     `json:"error"`
	Attempts            int        `json:"attempts"`
	LastSeen            time.Time  `json:"lastSeen"`
	UpdatedAt           *time.Time `json:"updatedAt"`
	Online              bool       `json:"online"`
}
type ConfigView struct {
	Revision
	Readonly        map[string]any `json:"readOnly"`
	SecurityPending bool           `json:"securityPending"`
	State           string         `json:"state"`
	Nodes           []ConfigNode   `json:"nodes"`
}

var ErrApplyFailed = errors.New("configuration preparation failed")

type ConfigurationValues map[string]any

func (v ConfigurationValues) String(key string) string { s, _ := v[key].(string); return s }
func validConfigurationText(value string, p Property) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < p.Min || utf8.RuneCountInString(value) > p.Max {
		return false
	}
	if p.Min > 0 && strings.TrimSpace(value) == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && !((p.Key == "ldap-ca-pem" || p.Key == "mail-ca-pem" || p.Key == "llm-shell-prompt" || p.Key == "llm-audit-prompt") && (r == '\n' || r == '\r' || ((p.Key != "mail-ca-pem" && p.Key != "ldap-ca-pem") && r == '\t'))) {
			return false
		}
	}
	return true
}

func (v ConfigurationValues) Int(key string) int       { n, _ := v[key].(int); return n }
func (v ConfigurationValues) Bool(key string) bool     { b, _ := v[key].(bool); return b }
func (v ConfigurationValues) Float(key string) float64 { n, _ := v[key].(float64); return n }

// Keep integers exact and typed after loading snapshots, including old history.
func (v *ConfigurationValues) UnmarshalJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var parsed map[string]any
	if err := decoder.Decode(&parsed); err != nil {
		return err
	}
	for key, value := range parsed {
		if number, ok := value.(json.Number); ok {
			if key == "llm-temperature" {
				n, e := number.Float64()
				if e != nil {
					return e
				}
				parsed[key] = n
				continue
			}
			n, err := strconv.Atoi(string(number))
			if err != nil {
				return err
			}
			parsed[key] = n
		}
	}
	*v = parsed
	return nil
}

func ValidateConfiguration(values ConfigurationValues) error {
	if e := validateOAuthServerConfiguration(values); e != nil {
		return e
	}
	if e := validateForwardConfiguration(values); e != nil {
		return e
	}
	if e := validateFederationConfiguration(values); e != nil {
		return e
	}
	if _, _, e := PasskeyConfiguration(values); e != nil {
		return e
	}
	if err := validateLDAPConfiguration(values); err != nil {
		return err
	}
	if len(values) != len(PropertySchema()) {
		return fault.Invalid
	}
	for _, p := range PropertySchema() {
		v, ok := values[p.Key]
		if !ok {
			return fault.Invalid
		}
		if p.Type == "secret" {
			if !validSecretReference(v) {
				return fault.Invalid
			}
			continue
		}
		if p.Type == "string" {
			text, ok := v.(string)
			if !ok || !validConfigurationText(text, p) {
				return fault.Invalid
			}
			continue
		}
		if p.Type == "boolean" {
			if _, ok := v.(bool); !ok {
				return fault.Invalid
			}
			continue
		}
		if p.Type == "number" {
			n, ok := v.(float64)
			if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < float64(p.Min) || n > float64(p.Max) || (n < 0 && n != -1) {
				return fault.Invalid
			}
			continue
		}
		n, ok := v.(int)
		if !ok || n < p.Min || n > p.Max || ((strings.HasSuffix(p.Key, "-saved-limit-days") || p.Key == "password-expiration-period" || p.Key == "access-max-idle-second") && n == 0) {
			return fault.Invalid
		}
	}
	if err := validatePasswordConfiguration(values); err != nil {
		return fault.Invalid
	}
	if err := validateListenerConfiguration(values); err != nil {
		return err
	}
	if err := validateMailConfiguration(values); err != nil {
		return err
	}
	if err := validateClientIP(values); err != nil {
		return err
	}
	if err := validateAIModelConfiguration(values); err != nil {
		return err
	}
	if err := validateAgentEndpoint(values.String("agent-gateway-endpoint")); err != nil {
		return err
	}
	return validateWatermark(values)
}
func (s *Service) Configuration(ctx context.Context, id int64) (v Revision, e error) {
	var raw []byte
	e = s.db.Row(ctx, `SELECT r.id,r.snapshot,r.changed_keys,coalesce(u.username,''),r.legacy_write,r.created_at FROM configuration_revisions r LEFT JOIN users u ON u.id=r.actor_id WHERE r.id=CASE WHEN $1::bigint=0 THEN (SELECT revision FROM configuration_head WHERE singleton=1) ELSE $1 END`, id).Scan(&v.ID, &raw, &v.ChangedKeys, &v.Actor, &v.Legacy, &v.CreatedAt)
	if e == nil {
		e = json.Unmarshal(raw, &v.Values)
	}
	return
}

// SaveConfiguration must run in the caller's transaction with the operation audit.
// Locking the head provides serialization even for legacy writers without ETags.
func (s *Service) SaveConfiguration(ctx context.Context, expected *int64, patch map[string]json.RawMessage, actor string) (Revision, error) {
	var zero Revision
	if !s.db.InTransaction(ctx) || len(patch) == 0 || len(patch) > len(PropertySchema()) {
		return zero, fault.Invalid
	}
	var current int64
	if e := s.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR UPDATE`).Scan(&current); e != nil {
		return zero, e
	}
	if expected != nil && *expected != current {
		return zero, fault.Conflict
	}
	previous, e := s.Configuration(ctx, current)
	if e != nil {
		return zero, e
	}
	_, forwardEnabledRequested := patch[ForwardEnabledKey]
	_, forwardRulesRequested := patch[ForwardRulesKey]
	oldFederation := map[string]string{}
	for _, key := range []string{"oidc-client-secret", "oidc-client-id", "oidc-issuer", "wechat-work-secret", "wechat-work-corp-id", "wechat-work-api-url"} {
		oldFederation[key] = previous.Values.String(key)
	}
	oldModelURL, oldProxyURL, oldModelKey := previous.Values.String("llm-base-url"), previous.Values.String("llm-proxy-url"), previous.Values.String("llm-api-key")
	oldLDAPURL, oldLDAPUser, oldLDAPKey := previous.Values.String("ldap-url"), previous.Values.String("ldap-user"), previous.Values.String("ldap-password")
	keys := []string{}
	for _, definition := range PropertySchema() {
		raw, ok := patch[definition.Key]
		if !ok {
			continue
		}
		delete(patch, definition.Key)
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return zero, fault.Invalid
		}
		var value any
		if definition.Type == "secret" {
			var reference string
			if json.Unmarshal(raw, &reference) != nil || !validSecretReference(reference) {
				return zero, fault.Invalid
			}
			if reference != "" {
				var exists bool
				if err := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM configuration_secrets WHERE id=$1 AND key=$2)`, reference, definition.Key).Scan(&exists); err != nil {
					return zero, err
				}
				if !exists {
					return zero, fault.Invalid
				}
			}
			value = reference
		} else if definition.Type == "number" {
			var n float64
			if json.Unmarshal(raw, &n) != nil {
				var text string
				if json.Unmarshal(raw, &text) != nil {
					return zero, fault.Invalid
				}
				var err error
				n, err = strconv.ParseFloat(text, 64)
				if err != nil {
					return zero, fault.Invalid
				}
			}
			value = n
		} else if definition.Type == "string" {
			var text string
			if json.Unmarshal(raw, &text) != nil || !validConfigurationText(text, definition) {
				return zero, fault.Invalid
			}
			if definition.Key == ForwardRulesKey {
				rules, e := ParseForwardRules(text)
				if e != nil {
					return zero, e
				}
				text = EncodeForwardRules(rules)
			}
			value = text
		} else if definition.Type == "boolean" {
			var b bool
			if json.Unmarshal(raw, &b) != nil {
				var text string
				if json.Unmarshal(raw, &text) != nil || (text != "true" && text != "false") {
					return zero, fault.Invalid
				}
				b = text == "true"
			}
			value = b
		} else {
			var number int
			if json.Unmarshal(raw, &number) != nil {
				var text string
				if json.Unmarshal(raw, &text) != nil {
					return zero, fault.Invalid
				}
				n, err := strconv.Atoi(text)
				if err != nil || strconv.Itoa(n) != text {
					return zero, fault.Invalid
				}
				number = n
			}
			if number < definition.Min || number > definition.Max {
				return zero, fault.Invalid
			}
			value = number
		}
		if previous.Values[definition.Key] != value {
			if definition.Key == LoginCaptchaKey && !hasCaptchaConfigurationProof(ctx) {
				return zero, fault.Denied
			}
			if IsOAuthServerConfigurationKey(definition.Key) && !hasOAuthServerConfigurationProof(ctx) {
				return zero, fault.Denied
			}
			if definition.Key == DatabaseBlockDMLKey && !hasDatabaseDMLConfigurationProof(ctx) {
				return zero, fault.Denied
			}
			if definition.Key == NativePasswordDisabledKey && !hasNativePasswordConfigurationProof(ctx) {
				return zero, fault.Denied
			}
			if IsAccessMFAConfigurationKey(definition.Key) && !hasAccessMFAConfigurationProof(ctx) {
				return zero, fault.Denied
			}
			if definition.Key == "recording-enabled" && !hasRecordingConfigurationProof(ctx) {
				return zero, fault.Denied
			}
			if IsForwardConfigurationKey(definition.Key) && !hasForwardConfigurationProof(ctx) {
				return zero, fault.Denied
			}
			if IsFederationConfigurationKey(definition.Key) && !hasFederationConfigurationProof(ctx) {
				return zero, fault.Denied
			}
			if IsOAuthLifetimeKey(definition.Key) && !hasOAuthLifetimeProof(ctx) {
				return zero, fault.Denied
			}
			if strings.HasPrefix(definition.Key, "passkey-") && !hasPasskeyConfigurationProof(ctx) {
				return zero, fault.Denied
			}
			if strings.HasPrefix(definition.Key, "ldap-") && !hasLDAPConfigurationProof(ctx) {
				return zero, fault.Denied
			}
			if definition.Key == "disable-password-login" && !hasLoginPolicyProof(ctx) {
				return zero, fault.Denied
			}
			if strings.HasSuffix(definition.Key, "-saved-limit-days") {
				n := value.(int)
				old := previous.Values.Int(definition.Key)
				if n > 0 && (old == -1 || n < old) {
					var previewed bool
					if e = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM audit_retention_previews WHERE actor_id=$1 AND revision=$2 AND key=$3 AND days=$4 AND expires_at>now())`, actor, current, definition.Key, n).Scan(&previewed); e != nil {
						return zero, e
					}
					if !previewed {
						return zero, ErrRetentionPreviewRequired
					}
				}
			}
			keys = append(keys, definition.Key)
			previous.Values[definition.Key] = value
		}
	}
	if len(patch) != 0 {
		return zero, fault.Invalid
	}
	if e = ValidateConfiguration(previous.Values); e != nil {
		return zero, e
	}
	if oldModelKey != "" && oldModelKey == previous.Values.String("llm-api-key") && (oldModelURL != previous.Values.String("llm-base-url") || oldProxyURL != previous.Values.String("llm-proxy-url")) {
		return zero, fault.Invalid
	}
	if logo := previous.Values.String("system-logo"); logo != "" {
		var exists bool
		if e = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM brand_images WHERE id::text=$1)`, logo).Scan(&exists); e != nil {
			return zero, e
		}
		if !exists {
			return zero, fault.Invalid
		}
	}
	for _, key := range OAuthKeyRotations(ctx) {
		if (key != OAuthSigningKey && key != OAuthHMACKey) || !hasOAuthServerConfigurationProof(ctx) {
			return zero, fault.Denied
		}
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		// Re-saving an enabled target explicitly refreshes server DNS pins. Publish
		// a new revision even when the public host/port text did not change.
		if (forwardEnabledRequested || forwardRulesRequested) && previous.Values.Bool(ForwardEnabledKey) && hasForwardConfigurationProof(ctx) {
			keys = append(keys, ForwardRulesKey)
		} else {
			return previous, nil
		}
	}
	for _, key := range keys {
		if IsOAuthServerEntryKey(key) {
			if e = s.applyOAuthServerConfiguration(ctx, previous.Values); e != nil {
				return zero, e
			}
			break
		}
	}
	for _, key := range keys {
		if IsForwardConfigurationKey(key) {
			if e = s.applyForwardConfiguration(ctx, previous.Values); e != nil {
				return zero, e
			}
			break
		}
	}
	for _, key := range keys {
		if isFederationDepartmentKey(key) {
			if e = s.applyFederationDepartment(ctx, key, previous.Values.String(key)); e != nil {
				return zero, e
			}
		}
	}
	for _, pair := range [][4]string{{"oidc", "oidc-client-secret", "oidc-client-id", "oidc-issuer"}, {"wecom", "wechat-work-secret", "wechat-work-corp-id", "wechat-work-api-url"}} {
		if oldFederation[pair[1]] != "" && oldFederation[pair[1]] == previous.Values.String(pair[1]) && (oldFederation[pair[2]] != previous.Values.String(pair[2]) || oldFederation[pair[3]] != previous.Values.String(pair[3])) {
			return zero, fault.Invalid
		}
		for _, key := range keys {
			if !isFederationDepartmentKey(key) && ((pair[0] == "oidc" && strings.HasPrefix(key, "oidc-") && !strings.HasPrefix(key, "oidc-server-")) || (pair[0] == "wecom" && strings.HasPrefix(key, "wechat-work-"))) {
				if e = s.applyFederationConfiguration(ctx, previous.Values, pair[0]); e != nil {
					return zero, e
				}
				break
			}
		}
	}
	for _, key := range keys {
		if strings.HasPrefix(key, "passkey-") {
			if _, e = s.db.Exec(ctx, `DELETE FROM passkey_ceremonies`); e != nil {
				return zero, e
			}
			break
		}
	}
	if slices.Contains(keys, LoginCaptchaKey) {
		if _, e = s.db.Exec(ctx, `DELETE FROM auth_challenges WHERE purpose IN ('login','login-proof')`); e != nil {
			return zero, e
		}
	}
	if oldLDAPKey != "" && oldLDAPKey == previous.Values.String("ldap-password") && (oldLDAPURL != previous.Values.String("ldap-url") || oldLDAPUser != previous.Values.String("ldap-user")) {
		return zero, fault.Invalid
	}
	for _, key := range keys {
		if strings.HasPrefix(key, "ldap-") {
			if e = s.applyLDAPConfiguration(ctx, previous.Values); e != nil {
				return zero, e
			}
			break
		}
	}
	modelChanged := false
	for _, key := range keys {
		for _, modelKey := range aiModelKeys {
			if key == modelKey {
				modelChanged = true
			}
		}
	}
	if modelChanged {
		if e = s.applyAIModelConfiguration(ctx, previous.Values); e != nil {
			return zero, e
		}
	}
	for _, key := range keys {
		if strings.HasPrefix(key, "llm-") {
			if e = s.applyAIConfiguration(ctx, previous.Values); e != nil {
				return zero, e
			}
			break
		}
	}
	for _, key := range keys {
		if key == "disable-password-login" {
			if _, e = s.db.Exec(ctx, `UPDATE identity_login_policy SET password_enabled=$1,version=version+1 WHERE singleton=1`, !previous.Values.Bool(key)); e != nil {
				return zero, e
			}
		}
	}
	for _, key := range keys {
		if key == "ip-extractor" || key == "ip-trust-list" {
			prefixes, e := TrustedProxyPrefixes(previous.Values.String("ip-trust-list"))
			if e != nil {
				return zero, e
			}
			list := []string{}
			for _, p := range prefixes {
				list = append(list, p.String())
			}
			if _, e = s.db.Exec(ctx, `UPDATE website_settings SET api_trusted_proxies=$1,version=version+1`, list); e != nil {
				return zero, e
			}
			break
		}
	}
	for _, key := range keys {
		if strings.HasPrefix(key, "mail-") {
			if err := s.applyMailConfiguration(ctx, previous.Values); err != nil {
				return zero, err
			}
			break
		}
	}
	for _, key := range keys {
		if IsAccessMFAConfigurationKey(key) {
			if _, err := s.db.Exec(ctx, `DELETE FROM session_access_proofs`); err != nil {
				return zero, err
			}
			break
		}
	}
	raw, e := json.Marshal(previous.Values)
	if e != nil {
		return zero, e
	}
	var id int64
	if e = s.db.Row(ctx, `INSERT INTO configuration_revisions(snapshot,changed_keys,actor_id,legacy_write) VALUES($1,$2,NULLIF($3,'')::uuid,$4) RETURNING id`, raw, keys, actor, expected == nil).Scan(&id); e != nil {
		return zero, e
	}
	if _, e = s.db.Exec(ctx, `UPDATE configuration_head SET revision=$1 WHERE singleton=1`, id); e != nil {
		return zero, e
	}
	if e = s.publishSecurityConfiguration(ctx, id, previous.Values); e != nil {
		return zero, e
	}
	for _, key := range keys {
		value, err := json.Marshal(previous.Values[key])
		if err != nil {
			return zero, err
		}
		if _, e = s.db.Exec(ctx, `UPDATE settings SET typed_value=$2::jsonb,revision=revision+1 WHERE key=$1`, key, value); e != nil {
			return zero, e
		}
		if key == "access-log-saved-limit-days" {
			if _, e = s.db.Exec(ctx, `UPDATE settings SET typed_value=$1::jsonb,revision=revision+1 WHERE key='access-retention-days'`, value); e != nil {
				return zero, e
			}
		}
	}
	return s.Configuration(ctx, id)
}
func (s *Service) ConfigurationView(ctx context.Context, id int64) (view ConfigView, e error) {
	view.Revision, e = s.Configuration(ctx, id)
	if e != nil {
		return
	}
	if view.Readonly, e = s.ReadonlyConfiguration(ctx); e != nil {
		return view, e
	}
	if e = s.db.Row(ctx, `SELECT coalesce((SELECT pending FROM configuration_security WHERE revision=$1),false)`, view.ID).Scan(&view.SecurityPending); e != nil {
		return view, e
	}
	rows, e := s.db.Rows(ctx, `SELECT n.id,coalesce(a.state,'requested'),coalesce(a.error,''),coalesce(a.attempts,0),n.last_seen,a.updated_at,n.last_seen>now()-interval '30 seconds',coalesce((SELECT max(applied.revision) FROM configuration_acks applied WHERE applied.node_id=n.id AND applied.state='effective'),0) FROM configuration_nodes n LEFT JOIN configuration_acks a ON a.node_id=n.id AND a.revision=$1 ORDER BY n.id`, view.ID)
	if e != nil {
		return view, e
	}
	defer rows.Close()
	view.Nodes = []ConfigNode{}
	online, effective, failed := 0, 0, false
	for rows.Next() {
		var n ConfigNode
		if e = rows.Scan(&n.ID, &n.State, &n.Error, &n.Attempts, &n.LastSeen, &n.UpdatedAt, &n.Online, &n.LastAppliedRevision); e != nil {
			return view, e
		}
		view.Nodes = append(view.Nodes, n)
		if n.Online {
			online++
			if n.State == "effective" {
				effective++
			}
			failed = failed || n.State == "failed"
		}
	}
	view.State = "requested"
	if online > 0 {
		view.State = "applying"
	}
	if online > 0 && online == effective {
		view.State = "effective"
	}
	if failed {
		view.State = "failed"
	}
	if view.State != "effective" && time.Since(view.CreatedAt) > 30*time.Second {
		if !failed {
			view.State = "stalled"
		}
	}
	return view, rows.Err()
}

// Read, prepare and ACK are serialized with revision writes. Consumers must use
// this method again for every new connection: a stale/disconnected node cannot
// accept new work based on an old in-memory value.
func (s *Service) ApplyConfiguration(ctx context.Context, node string, prepare func(Revision) error) (v Revision, e error) {
	failed := false
	e = s.db.Within(ctx, func(tx context.Context) error {
		var id int64
		if e := s.db.Row(tx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR SHARE`).Scan(&id); e != nil {
			return e
		}
		if _, e := s.db.Exec(tx, `INSERT INTO configuration_nodes(id,last_seen) VALUES($1,now()) ON CONFLICT(id) DO UPDATE SET last_seen=now()`, node); e != nil {
			return e
		}
		var state string
		if e := s.db.Row(tx, `SELECT coalesce((SELECT state FROM configuration_acks WHERE revision=$1 AND node_id=$2),'requested')`, id, node).Scan(&state); e != nil {
			return e
		}
		if state == "failed" {
			failed = true
			return nil
		}
		var err error
		v, err = s.Configuration(tx, id)
		if err != nil {
			return err
		}
		if ValidateConfiguration(v.Values) != nil || prepare(v) != nil {
			failed = true
			state = "failed"
		} else {
			state = "effective"
		}
		message := ""
		if failed {
			message = "配置准备失败；现有会话保留原配置，新连接暂停接入。请检查配置后重试。"
		}
		_, err = s.db.Exec(tx, `INSERT INTO configuration_acks(revision,node_id,state,error,attempts) VALUES($1,$2,$3,$4,1) ON CONFLICT(revision,node_id) DO UPDATE SET state=excluded.state,error=excluded.error,attempts=configuration_acks.attempts+CASE WHEN configuration_acks.state='requested' OR (configuration_acks.state='effective' AND excluded.state='failed') THEN 1 ELSE 0 END,updated_at=now()`, id, node, state, message)
		if err == nil && !failed {
			err = s.activateSecurityConfiguration(tx, v)
		}
		return err
	})
	if e == nil && failed {
		e = ErrApplyFailed
	}
	return
}
func (s *Service) RetryConfiguration(ctx context.Context, id int64) error {
	var current int64
	if e := s.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR UPDATE`).Scan(&current); e != nil {
		return e
	}
	if current != id {
		return fault.Conflict
	}
	_, e := s.db.Exec(ctx, `UPDATE configuration_acks SET state='requested',error='',updated_at=now() WHERE revision=$1 AND state='failed'`, id)
	return e
}
