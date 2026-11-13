package application

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/rdpcert"
	"time"
)

func (a *App) discoverRDPCertificate(r *http.Request, in resource.Asset) (rdpcert.Certificate, error) {
	var empty rdpcert.Certificate
	u, e := a.require(r, "identity.manage")
	if e != nil {
		return empty, e
	}
	if in.Protocol != "rdp" {
		return empty, fault.Invalid
	}
	if in.RDPSecurity == "rdp" {
		return empty, problem(422, "rdp_legacy_no_tls", "传统 RDP 兼容模式不使用 TLS，无需抓取 TLS 证书")
	}
	// All normal asset fields are validated; only the as-yet unknown fingerprint is exempt.
	check := in
	check.CertFingerprint = "discovery"
	if e = resource.Validate(check); e != nil {
		return empty, e
	}
	if !a.allowAttempt("rdp-cert:" + u.ID) {
		return empty, problem(429, "rate_limited", "证书探测过于频繁")
	}
	select {
	case a.probeSlots <- struct{}{}:
		defer func() { <-a.probeSlots }()
	default:
		return empty, problem(429, "busy", "探测并发已满")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	authorize := func(c context.Context) error { _, e := a.require(r.WithContext(c), "identity.manage"); return e }
	request := "rdp-cert-" + identity.NewToken()[:32]
	var dial connectivity.Dialer = connectivity.DialTCP
	switch {
	case in.AgentID != "":
		dial = func(c context.Context, h string, p int) (net.Conn, error) {
			return a.connectivity.DialAgent(ctx, c, in.AgentID, request, h, p, authorize)
		}
	case in.JumpID != "":
		dial = a.jumpDialer(ctx, in.JumpID, u.ID, request)
	case in.GatewayGroupID != "":
		dial = a.groupDialer(ctx, in.GatewayGroupID, u.ID, request, authorize)
	case in.ProxyID != "":
		dial = a.proxyDialer(ctx, in.ProxyID, u.ID, request)
	}
	conn, e := dial(ctx, in.Host, in.Port)
	if e != nil {
		return empty, problem(422, "rdp_certificate_unreachable", "无法连接 RDP 目标，请检查地址、端口和连接路径")
	}
	defer conn.Close()
	cert, e := rdpcert.Probe(ctx, conn, in.Host)
	if e != nil {
		log.Printf("rdp_certificate_probe_failed request=%s host=%q port=%d error=%q", requestID(r), in.Host, in.Port, e)
		return empty, rdpCertificateProblem(e)
	}
	if e = authorize(ctx); e != nil {
		return empty, e
	}
	e = a.audit.Detail(ctx, u.ID, "rdp.certificate_discovered", requestID(r), sourceIP(r), map[string]string{"host": in.Host, "fingerprint": cert.Fingerprint})
	return cert, e
}

func rdpCertificateProblem(err error) error {
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return problem(422, "rdp_certificate_timeout", "RDP 证书探测超时，请检查目标响应及网关链路；此错误不代表远端未启用 TLS")
	}
	var failure *rdpcert.NegotiationFailure
	if errors.As(err, &failure) {
		if failure.Code == 2 {
			return problem(422, "rdp_legacy_required", "远端仅接受传统 RDP 安全层。如需兼容，请在资产的连接与认证中明确选择“传统 RDP 兼容模式”；此模式不提供 TLS 证书身份校验")
		}
		return problem(422, "rdp_security_negotiation_failed", fmt.Sprintf("远端拒绝 RDP 安全协商（代码 0x%08x），请核对远端 RDP 安全策略", failure.Code))
	}
	var probe *rdpcert.ProbeError
	if errors.As(err, &probe) && probe.Stage == "tls_handshake" {
		return problem(422, "rdp_tls_handshake_failed", "RDP 安全协商成功，但 TLS 握手失败；可能是加密套件不兼容、连接被关闭或证书异常，请查看服务端探测日志")
	}
	return problem(422, "rdp_certificate_failed", "未获得有效的 RDP TLS 协商响应，请检查目标是否为 RDP 端口及连接路径；详细原因见服务端探测日志")
}
func (a *App) rdpCertificate(w http.ResponseWriter, r *http.Request) error {
	var in resource.Asset
	if e := decode(w, r, &in); e != nil {
		return e
	}
	cert, e := a.discoverRDPCertificate(r, in)
	if e != nil {
		return e
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, cert)
	return nil
}
func (a *App) prepareRDPCertificate(r *http.Request, in *resource.Asset) error {
	if in.Protocol != "rdp" {
		return nil
	}
	if _, e := a.require(r, "identity.manage"); e != nil {
		return e
	}
	if in.ID != "" {
		rows, _, e := a.resources.List(r.Context(), resource.Filter{ID: in.ID, All: true, Limit: 1})
		if e != nil {
			return e
		}
		if len(rows) != 1 {
			return fault.Denied
		}
		old := rows[0]
		if old.Version != in.Version {
			return fault.Conflict
		}
		if old.Protocol != in.Protocol || old.Host != in.Host || old.Port != in.Port || old.AgentID != in.AgentID || old.JumpID != in.JumpID || old.ProxyID != in.ProxyID || old.GatewayGroupID != in.GatewayGroupID {
			in.CertFingerprint = ""
		}
	}
	if in.RDPSecurity == "rdp" {
		in.CertFingerprint = ""
		return resource.Validate(*in)
	}
	if in.RDPSecurity != "" && in.RDPSecurity != "nla" {
		return fault.Invalid
	}
	if in.CertFingerprint == "" {
		cert, e := a.discoverRDPCertificate(r, *in)
		if e != nil {
			return e
		}
		in.CertFingerprint = cert.Fingerprint
	}
	return nil
}
