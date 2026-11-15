package notification

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"puppeteer/backend/internal/platform/configstate"
	"puppeteer/backend/internal/platform/secretbox"
)

func (s *Service) Preflight(ctx context.Context, id string, report func(configstate.Check) bool) {
	check := func(code string, err error, good, bad string) bool {
		state, summary := "ready", good
		if err != nil {
			state, summary = "blocked", bad
		}
		return report(configstate.Check{Code: code, State: state, Summary: summary, Scope: "notification-channel", CheckedAt: time.Now().UTC()}) && err == nil
	}
	var kind, aad string
	var config, sealed []byte
	err := s.db.Row(ctx, `SELECT c.type,c.config,coalesce(s.sealed,c.secret),coalesce('settings:'||s.key||':'||s.id::text,'notification:'||c.id::text) FROM notification_channels c LEFT JOIN configuration_secrets s ON s.id=c.configuration_secret_id WHERE c.id=$1`, id).Scan(&kind, &config, &sealed, &aad)
	var c Config
	if err == nil {
		err = json.Unmarshal(config, &c)
	}
	var secret []byte
	if err == nil && len(sealed) > 0 {
		secret, err = secretbox.Open(s.key, sealed, aad)
	}
	defer clear(secret)
	if !check("channel_configuration", err, "渠道配置与凭据可读取", "渠道配置缺失或凭据无法解密，请重新保存后重试") {
		return
	}
	switch kind {
	case "smtp":
		ProbeSMTP(ctx, c, string(secret), report)
	case "wecom", "sms-webhook":
		if !check("channel_https", probeChannelHTTPS(ctx, c, string(secret), kind == "wecom"), "外部渠道 TLS 检查通过；企业微信仅校验应用凭据，Webhook 认证须显式投递验证", "外部渠道连接、TLS 或应用凭据校验失败；请检查地址、CA 与密钥") {
			return
		}
	default:
		check("channel_protocol", errTransport, "", "当前渠道协议不支持预检")
		return
	}
	var bindings, rules int
	err = s.db.Row(ctx, `SELECT (SELECT count(*) FROM notification_bindings WHERE channel_id=$1 AND enabled),(SELECT count(*) FROM notification_rules WHERE $1::uuid=ANY(channel_ids) AND enabled)`, id).Scan(&bindings, &rules)
	check("channel_routing", err, fmt.Sprintf("已启用接收绑定 %d 项、关联规则 %d 项；数量不证明事件匹配或投递成功，请查看规则与投递记录", bindings, rules), "无法读取接收绑定及规则状态")
}

func probeChannelHTTPS(ctx context.Context, c Config, secret string, wecom bool) error {
	u, err := url.Parse(c.APIBase)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || secret == "" {
		return errTransport
	}
	tc, err := transportTLS(c)
	if err != nil {
		return errTransport
	}
	tc.ServerName = u.Hostname()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if !wecom {
		port := u.Port()
		if port == "" {
			port = "443"
		}
		conn, err := (&tls.Dialer{NetDialer: &net.Dialer{Timeout: 8 * time.Second}, Config: tc}).DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
		if err != nil {
			return errTransport
		}
		conn.Close()
		return nil
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/cgi-bin/gettoken"
	u.RawQuery = url.Values{"corpid": {c.CorpID}, "corpsecret": {secret}}.Encode()
	transport := &http.Transport{TLSClientConfig: tc}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return errTransport
	}
	res, err := client.Do(req)
	if err != nil {
		return errTransport
	}
	defer res.Body.Close()
	var body struct {
		Code  int    `json:"errcode"`
		Token string `json:"access_token"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&body) != nil || body.Code != 0 || body.Token == "" {
		return errTransport
	}
	return nil
}
