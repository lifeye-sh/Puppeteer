// Package configstate separates user intent, configuration ACKs, dependency
// evidence and live observations. It never treats a feature flag as a probe.
package configstate

import "time"

type Feature struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Category string `json:"category"`
	Section  string `json:"section"`
	Key      string `json:"-"`
	Protocol string `json:"-"`
	Invert   bool   `json:"-"`
}

var Features = []Feature{
	{"mp4", "MP4 渲染与下载", "deployment", "mp4", "", "mp4", false},
	{"rdp-drive", "RDP 共享盘 / MyZone", "deployment", "drive", "", "rdp-drive", false},
	{"graphics", "浏览器 RDP / VNC", "deployment", "components", "", "graphics", false},
	{"mysql", "MySQL 代理", "deployment", "deployment", "db-proxy-enabled", "mysql", false},
	{"postgres", "PostgreSQL 代理", "deployment", "deployment", "postgres-proxy-enabled", "postgres", false},
	{"website", "网站 HTTPS 网关", "deployment", "deployment", "website-server-enabled", "website", false},
	{"ssh", "SSH 网关", "deployment", "deployment", "ssh-server-enabled", "ssh", false},
	{"management", "管理 HTTP", "deployment", "deployment", "", "management", false},
	{"native-rdp", "原生 RDP", "deployment", "listeners", "native-rdp-enabled", "", false},
	{"mail", "平台邮件", "dependencies", "mail", "mail-enabled", "", false},
	{"ldap", "LDAP 目录", "dependencies", "ldap", "ldap-enabled", "", false},
	{"oidcProvider", "OIDC 身份源", "dependencies", "oidcProvider", "oidc-enabled", "", false},
	{"wecomProvider", "企业微信身份源", "dependencies", "wecomProvider", "wechat-work-enabled", "", false},
	{"oauthServer", "OIDC 服务端", "dependencies", "oauthServer", "oidc-server-enabled", "", false},
	{"native-client", "原生客户端访问", "dependencies", "oauthServer", "native-client-enabled", "", false},
	{"passkey", "Passkey", "dependencies", "passkey", "passkey-enabled", "", false},
	{"aiModel", "默认模型连接", "dependencies", "aiModel", "llm-profile-enabled", "", false},
	{"forward", "SSH 受控转发", "dependencies", "forward", "ssh-server-port-forwarding-enabled", "", false},
	{"databasePolicy", "数据库原生 DML", "dependencies", "databasePolicy", "db-proxy-block-dml", "", true},
	{"login", "登录安全", "policies", "login", "", "", false},
	{"access", "远程会话", "policies", "access", "", "", false},
	{"ai", "AI 使用策略", "policies", "ai", "llm-enabled", "", false},
	{"recording", "会话录屏策略", "policies", "recording", "recording-enabled", "", false},
	{"watermark", "水印策略", "policies", "watermark", "", "", false},
	{"resources", "资源默认值", "policies", "resources", "", "", false},
	{"retention", "日志保留策略", "policies", "retention", "", "", false},
}

func Find(id string) (Feature, bool) {
	for _, f := range Features {
		if f.ID == id {
			return f, true
		}
	}
	return Feature{}, false
}

type Check struct {
	CheckedAt   time.Time `json:"checkedAt"`
	Code        string    `json:"code"`
	State       string    `json:"state"`
	Summary     string    `json:"summary"`
	Remediation string    `json:"remediation,omitempty"`
	Scope       string    `json:"scope"`
}

type Task struct {
	Mode       string    `json:"mode,omitempty"`
	Evidence   string    `json:"evidence,omitempty"`
	ID         string    `json:"id"`
	Feature    string    `json:"feature"`
	Node       string    `json:"node"`
	Revision   int64     `json:"revision"`
	Generation *uint64   `json:"generation,omitempty"`
	Actor      string    `json:"actor"`
	State      string    `json:"state"`
	Stage      string    `json:"stage"`
	Started    time.Time `json:"started"`
	Updated    time.Time `json:"updated"`
	Expires    time.Time `json:"expires"`
	Deadline   time.Time `json:"deadline"`
	Checks     []Check   `json:"checks"`
}

// Dependency is conservative across restarts, other nodes, revisions and host
// generations. A past successful task is not evidence of current service health.
func Dependency(t *Task, node string, revision int64, generation *uint64, now time.Time) string {
	if t == nil {
		return "unknown"
	}
	if t.Node != node || t.Revision != revision {
		return "stale"
	}
	if t.Generation != nil && (generation == nil || *t.Generation != *generation) {
		return "stale"
	}
	if t.State == "running" {
		if !now.Before(t.Deadline) {
			return "stale"
		}
		return "checking"
	}
	if t.State == "cancelled" || t.State == "interrupted" || t.State == "stale" || !now.Before(t.Expires) {
		return "stale"
	}
	if t.State == "succeeded" {
		return "ready"
	}
	return "blocked"
}

func Normalize(t Task, now time.Time) Task {
	if t.State == "running" && !now.Before(t.Deadline) {
		t.State, t.Stage = "interrupted", "interrupted"
		t.Checks = append(t.Checks, Check{Code: "check_interrupted", State: "blocked", Summary: "预检超时或服务进程已中断", Remediation: "重新执行只读预检；不会自动重放外部操作", Scope: "node", CheckedAt: now})
	}
	return t
}
