package pki

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/crypto/acme"
	"puppeteer/backend/internal/platform/configstate"
	"puppeteer/backend/internal/platform/secretbox"
)

// PreflightOrder discovers the directory and performs a signed read-only DNS
// query. It never registers an account, creates an order or writes TXT records.
func (s *Service) PreflightOrder(ctx context.Context, id string, report func(configstate.Check) bool) {
	check := func(code string, ok bool, good, bad string) bool {
		state, summary := "ready", good
		if !ok {
			state, summary = "blocked", bad
		}
		return report(configstate.Check{Code: code, State: state, Summary: summary, Scope: "acme-dependency", CheckedAt: time.Now().UTC()}) && ok
	}
	var directory, ca, providerID string
	var sealed []byte
	var accountID string
	err := s.db.Row(ctx, `SELECT a.id::text,a.directory_url,a.ca_pem,a.key_sealed,o.provider_id::text FROM certificate_orders o JOIN acme_accounts a ON a.id=o.account_id WHERE o.id=$1`, id).Scan(&accountID, &directory, &ca, &sealed, &providerID)
	if !check("acme_configuration", err == nil, "ACME 订单、账号与 DNS 引用存在", "订单或关联账号不存在，请重新选择配置") {
		return
	}
	der, err := secretbox.Open(s.key, sealed, "acme:"+accountID)
	defer clear(der)
	var key any
	if err == nil {
		key, err = x509.ParsePKCS8PrivateKey(der)
	}
	signer, ok := key.(crypto.Signer)
	if !check("acme_account_key", err == nil && ok, "ACME 账号签名密钥可用", "ACME 账号密钥不可读取，请检查密钥存储") {
		return
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !check("acme_ca", ca == "" || roots.AppendCertsFromPEM([]byte(ca)), "ACME CA 配置有效", "ACME CA 无法读取，请检查 PEM 证书") {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, MaxResponseHeaderBytes: 32768}
	defer transport.CloseIdleConnections()
	client := &acme.Client{Key: signer, DirectoryURL: directory, HTTPClient: &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	directoryInfo, err := client.Discover(ctx)
	validDirectory := err == nil
	for _, endpoint := range []string{directoryInfo.NonceURL, directoryInfo.RegURL, directoryInfo.OrderURL} {
		u, e := url.Parse(endpoint)
		validDirectory = validDirectory && e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
	}
	if !check("acme_directory", validDirectory, "ACME HTTPS 目录发现通过；未注册账号或提交签发", "ACME 目录或 TLS 访问失败，请检查地址和 CA") {
		return
	}
	p, err := s.dnsProvider(ctx, providerID)
	if !check("acme_dns_secret", err == nil, "RFC2136 DNS 凭据可读取", "DNS 凭据不可读取，请重新保存 TSIG 密钥") {
		return
	}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(p.Zone), dns.TypeSOA)
	m.SetTsig(p.KeyName, dns.HmacSHA256, 300, time.Now().Unix())
	res, _, err := (&dns.Client{Net: "tcp", Timeout: 5 * time.Second, TsigSecret: map[string]string{p.KeyName: p.Secret}}).ExchangeContext(ctx, m, p.Server)
	hasSOA := false
	if res != nil {
		for _, rr := range res.Answer {
			if soa, ok := rr.(*dns.SOA); ok && strings.EqualFold(soa.Hdr.Name, dns.Fqdn(p.Zone)) {
				hasSOA = true
			}
		}
	}
	if !check("acme_dns_query", err == nil && res != nil && res.Rcode == dns.RcodeSuccess && res.IsTsig() != nil && hasSOA, "DNS TCP、TSIG 及区域查询通过；TXT 写入和清理由实际签发验证", "DNS 签名查询失败，请检查服务器、区域及 TSIG；签名查询需得到签名应答") {
		return
	}
	var pending int
	err = s.db.Row(ctx, `SELECT count(*) FROM acme_dns_records WHERE order_id=$1`, id).Scan(&pending)
	check("acme_dns_cleanup", err == nil && pending == 0, "当前订单没有待清理的 DNS 挑战记录；续签仍以订单日志为准", "订单仍有待清理 DNS 记录或无法读取，请先处理签发日志中的清理失败")
}
