package notification

import (
	"context"
	"crypto/tls"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"puppeteer/backend/internal/platform/configstate"
)

// ProbeSMTP stops after TLS and authentication. It never sends MAIL, RCPT or
// DATA, so recipient acceptance and delivery remain an explicit separate test.
// Returning false from report stops the probe (for example a cancelled task).
func ProbeSMTP(ctx context.Context, c Config, password string, report func(configstate.Check) bool) {
	check := func(code string, ok bool, success, failure, remedy string) bool {
		state, summary := "ready", success
		if !ok {
			state, summary = "blocked", failure
		}
		return report(configstate.Check{Code: code, State: state, Summary: summary, Remediation: remedy, Scope: "smtp", CheckedAt: time.Now().UTC()}) && ok
	}
	_, err := mail.ParseAddress(c.From)
	valid := err == nil && !strings.ContainsAny(c.From+c.Host, "\r\n") && c.Host != "" && c.Port > 0 && c.Port <= 65535 && (c.Username == "" || password != "")
	if !check("smtp_configuration", valid, "SMTP 地址、发件人格式及凭据配置完整", "SMTP 地址、发件人或凭据不完整", "填写主机、端口、发件人；设置用户名时同时保存密码") {
		return
	}
	tc, err := transportTLS(c)
	if !check("smtp_ca", err == nil, "SMTP 信任根已加载", "SMTP CA 无法加载", "检查私有 CA PEM，不能跳过证书验证") {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	raw, err := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(c.Host, strconv.Itoa(c.Port)))
	if !check("smtp_network", err == nil, "SMTP DNS 解析及 TCP 连接通过", "SMTP 域名解析或 TCP 连接失败", "检查主机、端口与当前节点出站连接") {
		if raw != nil {
			raw.Close()
		}
		return
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	_ = raw.SetDeadline(deadline)
	var conn net.Conn = raw
	if c.ImplicitTLS {
		secure := tls.Client(raw, tc)
		err = secure.HandshakeContext(ctx)
		if !check("smtp_tls", err == nil, "SMTP 隐式 TLS 身份验证通过", "SMTP TLS 握手或证书验证失败", "检查 TLS 模式、证书有效期、主机名和 CA") {
			return
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, c.Host)
	if !check("smtp_greeting", err == nil, "SMTP 服务应答通过", "SMTP 服务应答失败", "确认端口提供 SMTP 协议") {
		return
	}
	defer client.Close()
	if !c.ImplicitTLS {
		ok, _ := client.Extension("STARTTLS")
		if !check("smtp_starttls", ok, "SMTP 支持 STARTTLS", "SMTP 未提供强制 STARTTLS", "启用服务端 STARTTLS 或选择隐式 TLS 端口") {
			return
		}
		err = client.StartTLS(tc)
		if !check("smtp_tls", err == nil, "SMTP STARTTLS 身份验证通过", "SMTP TLS 握手或证书验证失败", "检查证书有效期、主机名和 CA") {
			return
		}
	}
	if c.Username != "" {
		err = client.Auth(smtp.PlainAuth("", c.Username, password, c.Host))
		if !check("smtp_authentication", err == nil, "SMTP 账号认证通过", "SMTP 账号认证失败或认证机制不支持", "检查 SMTP 用户名、密码与服务端 PLAIN 认证支持") {
			return
		}
	} else {
		if !check("smtp_authentication", true, "未配置 SMTP 用户名；未验证匿名中继权限", "", "") {
			return
		}
	}
	check("smtp_probe_complete", true, "连接预检完成；未发送邮件，发件权限和实际投递尚未验证", "", "在通知渠道中显式执行测试投递并检查接收人和规则")
}
