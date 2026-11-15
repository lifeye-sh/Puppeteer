package ai

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"puppeteer/backend/internal/platform/configstate"
)

// ProbeEndpoint uses the exact IP policy and proxy route used for inference.
// It performs no HTTP request, sends no API key and incurs no model usage.
func ProbeEndpoint(ctx context.Context, p Profile) error {
	u, err := ParseEndpoint(p.BaseURL, p.AllowPrivate)
	if err != nil {
		return ErrProvider
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := dialProvider(ctx, p, u, net.JoinHostPort(u.Hostname(), endpointPort(u)))
	if err != nil {
		return ErrProvider
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if u.Scheme == "https" {
		if tls.Client(conn, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}).HandshakeContext(ctx) != nil {
			return ErrProvider
		}
	}
	return nil
}

func (s *Service) Preflight(ctx context.Context, id, actor string, report func(configstate.Check) bool) {
	check := func(code string, ok bool, good, bad, remedy string) bool {
		state, summary := "ready", good
		if !ok {
			state, summary = "blocked", bad
		}
		return report(configstate.Check{Code: code, State: state, Summary: summary, Remediation: remedy, Scope: "ai-dependency", CheckedAt: time.Now().UTC()}) && ok
	}
	p, sealed, err := s.profile(ctx, id)
	if !check("ai_profile", err == nil, "模型配置已读取", "模型配置不存在或无法读取", "先保存模型连接配置") {
		return
	}
	if err == nil {
		p, err = s.openProfile(p, sealed)
	}
	if !check("ai_credentials", err == nil, "模型凭据可读取；预检不向服务发送密钥", "模型密钥无法解密", "重新保存模型 API Key") {
		return
	}
	p.APIKey = ""
	if !check("ai_protocol", p.ProviderOptions.Valid() && p.Model != "", "模型协议及名称配置有效", "模型协议或名称不完整", "检查协议与模型名称") {
		return
	}
	if !check("ai_network", ProbeEndpoint(ctx, p) == nil, "模型地址的 DNS、出站策略、代理及 TCP/TLS 通过；尚未验证 API 认证或模型调用", "模型网络、代理或 TLS 检查失败", "检查 URL、内网授权、代理路径及证书；禁止跳过网络约束") {
		return
	}
	if !check("ai_entitlement", s.RequireExecution(ctx) == nil, "当前许可允许 AI 执行", "当前许可不允许 AI 执行", "检查许可有效期和 AI 能力授权") {
		return
	}
	policy, err := s.Settings(ctx)
	if !check("ai_policy", err == nil && policy.Enabled && p.Enabled, "平台和模型允许调用；工具仍受资产权限和审批限制", "AI 总开关或模型已停用，或策略无法读取", "检查 AI 使用策略与模型启用状态") {
		return
	}
	if !check("ai_pricing", (policy.DailyBudgetMicros == 0 && policy.MaxTurnCostMicros == 0) || (p.InputRate > 0 && p.OutputRate > 0), "预算所需计价参数完整", "启用预算限制后必须设置模型输入和输出价格", "填写服务协议对应的计价参数") {
		return
	}
	var used int64
	err = s.db.Row(ctx, `SELECT coalesce(sum(charged_micros),0) FROM ai_usage_ledger WHERE user_id=$1 AND created_at>=date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, actor).Scan(&used)
	check("ai_budget", err == nil && (policy.DailyBudgetMicros == 0 || used < policy.DailyBudgetMicros), "发起人的当日预算未耗尽；每次真实调用仍按输入重新预留和检查单次上限", "发起人的当日预算已耗尽或无法读取", "检查用量和预算；此检测不增加或预留额度")
}
