package application

import (
	"context"
	"fmt"
	"net"
	"puppeteer/backend/internal/modules/connectivity"
	"puppeteer/backend/internal/modules/identity"
	"puppeteer/backend/internal/platform/configstate"
	"puppeteer/backend/internal/platform/fault"
	"time"
)

func (a *App) connectivityDependencyCheck(ctx context.Context, t configstate.Task, kind, id string, report func(configstate.Check) bool) {
	check := func(code string, ok bool, good, bad string) bool {
		state, summary := "ready", good
		if !ok {
			state, summary = "blocked", bad
		}
		return report(configstate.Check{Code: code, State: state, Summary: summary, Scope: "registered-route", CheckedAt: time.Now().UTC()}) && ok
	}
	authorize := func(c context.Context) error {
		var valid bool
		if e := a.db.Row(c, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND type='admin' AND status='enabled' AND deleted_at IS NULL)`, t.Actor).Scan(&valid); e != nil || !valid {
			return fault.Denied
		}
		e := a.allowed(c, &identity.User{ID: t.Actor, Type: "admin"}, "identity.manage")
		if e != nil {
			return fault.Denied
		}
		return nil
	}
	if !check("route_authorization", authorize(ctx) == nil, "管理员诊断权限有效；不授予业务会话权限", "发起人已停用或失去诊断权限") {
		return
	}
	if kind == "agent-gateway" {
		if !check("agent_channel", a.connectivity.ValidateDevice(ctx, id) == nil && a.connectivity.AgentDialReady(id), "Agent 身份与实时拨号通道就绪", "Agent 已撤销、离线或不支持拨号；请查看已有安装任务，不会自动安装远程服务") {
			return
		}
		observations, e := a.connectivity.Observations(ctx)
		fresh := false
		for _, o := range observations {
			if o.ID == id {
				fresh = o.Online && !o.Stale
			}
		}
		if !check("agent_heartbeat", e == nil && fresh, "Agent 采集心跳未过期", "Agent 采集心跳未就绪或过期，请检查设备和安装任务") {
			return
		}
	}
	column := map[string]string{"agent-gateway": "agent_id", "ssh-jump": "jump_id", "gateway-group": "gateway_group_id", "network-proxy": "proxy_id"}[kind]
	if column == "" {
		return
	}
	// Only targets already registered against this resource can be probed.
	rows, e := a.db.Rows(ctx, `SELECT a.id::text,a.host,a.port,r.version FROM assets a JOIN resource_registry r ON r.id=a.id WHERE a.`+column+`=$1 AND r.deleted_at IS NULL AND r.enabled ORDER BY a.id LIMIT 65`, id)
	type target struct {
		id, host string
		port     int
		version  int64
	}
	targets := []target{}
	if e == nil {
		for rows.Next() {
			var v target
			if e = rows.Scan(&v.id, &v.host, &v.port, &v.version); e != nil {
				break
			}
			targets = append(targets, v)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
	}
	if !check("route_references", e == nil && len(targets) <= 64, "资产引用已读取", "资产引用不可读取或超过单次 64 项上限，请分组检查") {
		return
	}
	count := 0
	for _, v := range targets {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		var dial connectivity.Dialer
		lease := "dependency-" + t.ID
		switch kind {
		case "agent-gateway":
			dial = func(c context.Context, h string, p int) (net.Conn, error) {
				return a.connectivity.DialAgent(ctx, c, id, lease, h, p, authorize)
			}
		case "ssh-jump":
			dial = a.jumpDialer(ctx, id, t.Actor, lease)
		case "gateway-group":
			dial = a.groupDialer(ctx, id, t.Actor, lease, authorize)
		case "network-proxy":
			dial = a.proxyDialer(ctx, id, t.Actor, lease)
		}
		e = authorize(c)
		if e == nil {
			var conn net.Conn
			conn, e = dial(c, v.host, v.port)
			if conn != nil {
				conn.Close()
			}
		}
		cancel()
		count++
		if !check(fmt.Sprintf("route_target_%d", count), e == nil, fmt.Sprintf("引用资产 %d 通过实际路由 TCP 检查；未登录目标", count), fmt.Sprintf("引用资产 %d 的路由、凭据、主机信任或目标连接失败；不会回退直连", count)) {
			return
		}
	}
	sites, e := a.websites().List(ctx)
	if !check("route_websites", e == nil, "网站引用已读取", "网站引用无法读取") {
		return
	}
	for _, v := range sites {
		route := map[string]string{"agent-gateway": v.AgentID, "ssh-jump": v.JumpID, "gateway-group": v.GatewayGroupID, "network-proxy": v.ProxyID}[kind]
		if route != id {
			continue
		}
		count++
		if !check(fmt.Sprintf("route_target_%d", count), a.probeWebsiteRoute(ctx, v, t.Actor) == nil, fmt.Sprintf("引用网站 %d 通过实际路由 TCP/TLS 检查；未发送 HTTP 请求", count), fmt.Sprintf("引用网站 %d 的实际路由或 TLS 不可用；请检查资源配置", count)) {
			return
		}
	}
	check("route_complete", count > 0, fmt.Sprintf("已检查 %d 个已登记引用；新会话仍需资产授权", count), "没有已登记的目标引用，无法验证完整转发链路；先关联资产或网站再预检")
}

func (a *App) connectivityDependencyEvidence(ctx context.Context) (string, error) {
	var graph string
	err := a.db.Row(ctx, `SELECT jsonb_build_array(
 (SELECT coalesce(jsonb_agg(jsonb_build_array(id,credential_version,connection_epoch,revoked_at) ORDER BY id),'[]') FROM agent_gateway_configs),
 (SELECT coalesce(jsonb_agg(jsonb_build_array(id,version) ORDER BY id),'[]') FROM ssh_jump_hosts),
 (SELECT coalesce(jsonb_agg(jsonb_build_array(id,version) ORDER BY id),'[]') FROM gateway_groups),
 (SELECT coalesce(jsonb_agg(jsonb_build_array(id,version) ORDER BY id),'[]') FROM network_proxies),
 (SELECT coalesce(jsonb_agg(jsonb_build_array(id,version,deleted_at) ORDER BY id),'[]') FROM resource_registry),
 (SELECT coalesce(jsonb_agg(jsonb_build_array(id,version,active_version,deleted_at) ORDER BY id),'[]') FROM credentials)
 )::text`).Scan(&graph)
	return graph, err
}
