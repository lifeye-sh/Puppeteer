package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/operations"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"time"
)

func (a *App) routesR16Diagnostics(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("GET /api/admin/diagnostics/source", func(w http.ResponseWriter, r *http.Request) error {
		if _, err := a.require(r, "identity.manage"); err != nil {
			return err
		}
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			peer = r.RemoteAddr
		}
		trusted, _ := r.Context().Value(trustedIPAppliedKey{}).(bool)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"sourceIp": sourceIP(r), "peerIp": peer, "trustedProxyApplied": trusted, "description": "来源 IP 由平台可信代理策略提取；不代表浏览器所在网络的公网地址。"})
		return nil
	})
	h("POST /api/admin/diagnostics/stream", a.diagnosticStream)
}

func (a *App) diagnosticStream(w http.ResponseWriter, r *http.Request) error {
	u, err := a.require(r, "identity.manage")
	if err != nil {
		return err
	}
	var in struct {
		AssetID string `json:"assetId"`
		Kind    string `json:"kind"`
		Count   int    `json:"count"`
	}
	if err = decode(w, r, &in); err != nil {
		return err
	}
	if (in.Kind != "ping" && in.Kind != "tcp") || in.Count < 1 || in.Count > 10 {
		return fault.Invalid
	}
	asset, err := a.sessionAsset(r.Context(), u, in.AssetID)
	if err != nil {
		return err
	}
	indirect := asset.AgentID != "" || asset.JumpID != "" || asset.ProxyID != "" || asset.GatewayGroupID != ""
	if in.Kind == "ping" && indirect {
		return problem(422, "icmp_route_unsupported", "ICMP 仅支持直连资产；当前连接路径请使用 TCP 诊断")
	}
	if !a.allowAttempt("diagnostics:" + u.ID) {
		return problem(429, "rate_limited", "诊断请求过于频繁，请稍后重试")
	}
	select {
	case a.probeSlots <- struct{}{}:
		defer func() { <-a.probeSlots }()
	default:
		return problem(429, "busy", "诊断并发已满，请稍后重试")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	check := func(c context.Context) error {
		current, e := a.require(r.WithContext(c), "identity.manage")
		if e != nil {
			return e
		}
		latest, e := a.sessionAsset(c, current, asset.ID)
		if e != nil {
			return e
		}
		if latest.Version != asset.Version {
			return fault.Denied
		}
		return nil
	}
	var ip net.IP
	if !indirect {
		c, done := context.WithTimeout(ctx, 3*time.Second)
		ip, err = operations.DiagnosticIP(c, asset.Host)
		done()
		if err != nil {
			return problem(422, "diagnostic_target_invalid", "目标无法解析或不允许探测此地址")
		}
	}
	lease := "diagnostic-" + identity.NewToken()[:32]
	route := "direct"
	transport := connectivity.DialTCP
	if asset.AgentID != "" {
		route = "agent"
		transport = func(c context.Context, h string, p int) (net.Conn, error) {
			return a.connectivity.DialAgent(ctx, c, asset.AgentID, lease, h, p, check)
		}
	}
	if asset.JumpID != "" {
		route = "ssh-jump"
		transport = a.jumpDialer(ctx, asset.JumpID, u.ID, lease)
	}
	if asset.GatewayGroupID != "" {
		route = "gateway-group"
		transport = a.groupDialer(ctx, asset.GatewayGroupID, u.ID, lease, check)
	}
	if asset.ProxyID != "" {
		route = "proxy"
		transport = a.proxyDialer(ctx, asset.ProxyID, u.ID, lease)
	}
	if err = a.audit.Detail(ctx, u.ID, "diagnostic.started", requestID(r), sourceIP(r), map[string]string{"assetId": asset.ID, "kind": in.Kind, "count": strconv.Itoa(in.Count), "route": route}); err != nil {
		return err
	}
	result := "cancelled"
	sent, succeeded := 0, 0
	defer func() {
		c, done := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
		defer done()
		_ = a.audit.Detail(c, u.ID, "diagnostic.completed", requestID(r), sourceIP(r), map[string]string{"assetId": asset.ID, "kind": in.Kind, "result": result, "sent": strconv.Itoa(sent), "succeeded": strconv.Itoa(succeeded)})
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	send := func(event string, value any) bool {
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		data, _ := json.Marshal(value)
		if _, e := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); e != nil {
			return false
		}
		return controller.Flush() == nil
	}
	if !send("start", map[string]any{"assetId": asset.ID, "kind": in.Kind, "route": route, "count": in.Count}) {
		return nil
	}
	for sequence := 1; sequence <= in.Count; sequence++ {
		c, done := context.WithTimeout(ctx, 3*time.Second)
		if err = check(c); err != nil {
			done()
			result = "authorization_changed"
			send("error", map[string]string{"message": "身份、资产或授权已变化，诊断终止"})
			return nil
		}
		started := time.Now()
		if in.Kind == "ping" {
			err = operations.Ping(c, ip, sequence)
		} else {
			host := asset.Host
			if ip != nil {
				host = ip.String()
			}
			var conn net.Conn
			conn, err = transport(c, host, asset.Port)
			if conn != nil {
				_ = conn.Close()
			}
		}
		done()
		if ctx.Err() != nil {
			return nil
		}
		sent++
		if err == nil {
			succeeded++
		}
		status := "reachable"
		message := "连接成功"
		if err != nil {
			status = "unreachable"
			message = "未收到响应或连接被拒绝；不代表目标已关机"
		}
		if errors.Is(err, operations.ErrPingUnavailable) {
			status = "unsupported"
			message = "运行环境未开放非特权 ICMP；请使用 TCP 诊断或检查 ping_group_range"
		}
		if !send("sample", map[string]any{"sequence": sequence, "ok": err == nil, "status": status, "durationMs": float64(time.Since(started).Microseconds()) / 1000, "message": message}) {
			return nil
		}
		if status == "unsupported" {
			result = "unsupported"
			break
		}
		if sequence < in.Count {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
		}
	}
	if result != "unsupported" {
		result = "completed"
	}
	send("complete", map[string]any{"result": result, "sent": sent, "succeeded": succeeded, "failed": sent - succeeded})
	return nil
}
