package application

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/database"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"time"
)

func (a *App) sessionAsset(ctx context.Context, u *identity.User, id string) (resource.Asset, error) {
	if !resource.ValidID(id) {
		return resource.Asset{}, fault.Invalid
	}
	f := resource.Filter{ID: id, EnabledOnly: true, All: u.Type == "admin", Limit: 1}
	if !f.All {
		deps, e := a.identity.DepartmentIDs(ctx, u.ID)
		if e != nil {
			return resource.Asset{}, e
		}
		f.Targets, e = a.authz.Targets(ctx, u.ID, deps)
		if e != nil {
			return resource.Asset{}, e
		}
	}
	items, _, e := a.resources.List(ctx, f)
	if e != nil {
		return resource.Asset{}, e
	}
	if len(items) != 1 {
		return resource.Asset{}, fault.Denied
	}
	return items[0], nil
}
func (a *App) hostKey(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "identity.manage"); e != nil {
		return e
	}
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return fault.Invalid
	}
	v, e := a.connectivity.Get(r.Context(), id)
	if errors.Is(e, pgx.ErrNoRows) {
		writeJSON(w, 200, (*connectivity.Trust)(nil))
		return nil
	}
	if e != nil {
		return e
	}
	writeJSON(w, 200, v)
	return nil
}
func (a *App) discoverHostKey(w http.ResponseWriter, r *http.Request) error {
	u, e := a.require(r, "identity.manage")
	if e != nil {
		return e
	}
	if !a.allowAttempt("ssh-discovery:" + sourceIP(r)) {
		return problem(429, "rate_limited", "请稍后再探测")
	}
	select {
	case a.probeSlots <- struct{}{}:
		defer func() { <-a.probeSlots }()
	default:
		return problem(429, "busy", "探测并发已满")
	}
	asset, e := a.sessionAsset(r.Context(), u, r.PathValue("id"))
	if e != nil {
		return e
	}
	if asset.Protocol != "ssh" {
		return problem(409, "protocol_unsupported", "只有 SSH 资产使用主机密钥核验")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var transport connectivity.Dialer
	if asset.AgentID != "" {
		leaseID := "probe-" + identity.NewToken()[:32]
		transport = func(dc context.Context, h string, p int) (net.Conn, error) {
			return a.connectivity.DialAgent(ctx, dc, asset.AgentID, leaseID, h, p, func(c context.Context) error {
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
			})
		}
	}
	if asset.JumpID != "" {
		transport = a.jumpDialer(ctx, asset.JumpID, u.ID, "jump-probe")
	}
	if asset.GatewayGroupID != "" {
		transport = a.groupDialer(ctx, asset.GatewayGroupID, u.ID, "probe-"+identity.NewToken()[:32], func(c context.Context) error {
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
		})
	}
	if asset.ProxyID != "" {
		transport = a.proxyDialer(ctx, asset.ProxyID, u.ID, "proxy-probe")
	}
	v, e := connectivity.DiscoverUsing(ctx, asset.Host, asset.Port, transport)
	if e != nil {
		return problem(422, "ssh_discovery_failed", "无法读取 SSH 主机密钥，请检查地址、端口和网络")
	}
	v.AssetID = asset.ID
	old, e := a.connectivity.Get(r.Context(), asset.ID)
	if e == nil {
		v.Version = old.Version
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if e = a.audit.Detail(r.Context(), u.ID, "ssh.host_key_observed", requestID(r), sourceIP(r), map[string]string{"assetId": asset.ID, "fingerprint": v.Fingerprint}); e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"observed": v, "trusted": old, "assetVersion": asset.Version})
	return nil
}
func (a *App) trustHostKey(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		connectivity.Trust
		AssetVersion int64 `json:"assetVersion"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	e := a.mutate(r, "ssh.host_key_trusted", func(ctx context.Context, u *identity.User) (string, error) {
		asset, e := a.sessionAsset(ctx, u, r.PathValue("id"))
		if e != nil {
			return "", e
		}
		if asset.Protocol != "ssh" {
			return "", fault.Invalid
		}
		if in.AssetVersion != asset.Version || in.Host != asset.Host || in.Port != asset.Port {
			return "", fault.Conflict
		}
		in.AssetID = asset.ID
		if e = a.connectivity.Save(ctx, in.Trust, u.ID); e != nil {
			return "", e
		}
		// Keep the exact new fingerprint in the audit evidence, not just the asset ID.
		saved, e := a.connectivity.Get(ctx, asset.ID)
		if e != nil {
			return "", e
		}
		e = a.audit.Detail(ctx, u.ID, "ssh.host_key_fingerprint", requestID(r), sourceIP(r), map[string]string{"assetId": asset.ID, "fingerprint": saved.Fingerprint})
		return asset.ID, e
	})
	if e != nil {
		return e
	}
	w.WriteHeader(204)
	return nil
}
func (a *App) reserveSession(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	if !a.allowAttempt("ssh-create:" + u.ID) {
		return problem(429, "rate_limited", "会话创建过于频繁")
	}
	var in struct {
		AssetID  string `json:"assetId"`
		Readonly bool   `json:"readonly"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	asset, e := a.sessionAsset(r.Context(), u, in.AssetID)
	if e != nil {
		return e
	}
	d, e := a.policyDecision(r.Context(), u, asset.ID, sourceIP(r), "connect", "")
	if e != nil {
		return e
	}
	if !d.Allowed {
		return problem(403, d.Reason, "访问策略拒绝连接")
	}
	if d.Filtered && asset.Protocol != "ssh" {
		return problem(403, "command_filter_protocol", "命令过滤仅支持 SSH 受控执行，拒绝此协议组合")
	}
	if (asset.Protocol == "rdp" || asset.Protocol == "vnc") && (a.config.GuacdAddr == "" || a.config.GuacdRelayHost == "") {
		return problem(503, "graphics_unavailable", "图形连接服务未配置")
	}
	ticket := identity.NewToken()
	var id string
	var initialError error
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		cred, e := a.vault.Get(ctx, asset.CredentialID)
		if e != nil {
			return e
		}
		var host connectivity.Trust
		if asset.Protocol == "ssh" {
			host, e = a.connectivity.Get(ctx, asset.ID)
			if errors.Is(e, pgx.ErrNoRows) {
				initialError = problem(409, "host_key_untrusted", "请先由管理员核对并信任此资产的 SSH 主机密钥")
			} else if e != nil {
				return e
			} else if host.Host != asset.Host || host.Port != asset.Port {
				initialError = problem(409, "host_key_endpoint_changed", "主机地址已变化，请重新核对主机密钥")
			}
		}
		id, e = a.sessions.Reserve(ctx, session.Session{ClientIP: sourceIP(r), Protocol: asset.Protocol, ActorID: u.ID, AssetID: asset.ID, AssetVersion: asset.Version, CredentialVersion: cred.Version, HostKeyVersion: host.Version, Readonly: in.Readonly}, identity.Digest(ticket), identity.Digest(token(r)))
		if e != nil {
			return e
		}
		if e = a.audit.OpenAccess(ctx, id, u.ID, asset.ID, u.Username, asset.Name, fmt.Sprintf("%s:%d", asset.Host, asset.Port), sourceIP(r)); e != nil {
			return e
		}
		if asset.Protocol == "rdp" || asset.Protocol == "vnc" {
			if cred.Kind != "password" {
				return fault.Invalid
			}
			if _, e = a.db.Exec(ctx, `UPDATE recordings SET format='guacamole-events-v1' WHERE session_id=$1`, id); e != nil {
				return e
			}
		}
		if initialError != nil {
			if e = a.sessions.Finish(ctx, id, "failed", "host_key_untrusted"); e != nil {
				return e
			}
			return a.audit.AccessState(ctx, id, "failed", "host_key_untrusted", true)
		}
		return a.audit.Detail(ctx, u.ID, "session.reserved", requestID(r), sourceIP(r), map[string]string{"sessionId": id, "assetId": asset.ID})
	})
	if e != nil {
		return e
	}
	if initialError != nil {
		return initialError
	}
	reserved, e := a.sessions.Get(r.Context(), id)
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"id": id, "ticket": ticket, "expiresIn": 30, "readonly": in.Readonly, "watermark": reserved.Watermark})
	return nil
}
func (a *App) inspectSession(r *http.Request, permission string) (session.Session, *identity.User, error) {
	u, e := a.authenticated(r)
	if e != nil {
		return session.Session{}, nil, e
	}
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return session.Session{}, u, fault.Invalid
	}
	v, e := a.sessions.Get(r.Context(), id)
	if e != nil {
		return v, u, e
	}
	if v.ActorID != u.ID {
		if e = a.allowed(r.Context(), u, permission); e != nil {
			return v, u, e
		}
	}
	return v, u, nil
}
func (a *App) listSessions(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	actor := u.ID
	if r.URL.Query().Get("all") == "true" {
		if e = a.allowed(r.Context(), u, "session.read"); e != nil {
			return e
		}
		actor = ""
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("current"))
	if page < 1 {
		page = 1
	}
	if page > 1000000 {
		return fault.Invalid
	}
	items, total, e := a.sessions.List(r.Context(), actor, r.URL.Query().Get("online") == "true", (page-1)*20, 20)
	if e != nil {
		return e
	}
	out := []map[string]any{}
	for _, v := range items {
		access, e := a.audit.Access(r.Context(), v.ID)
		if errors.Is(e, pgx.ErrNoRows) {
			continue
		} // Retention may have removed an ended record after the list snapshot.
		if e != nil {
			return e
		}
		out = append(out, map[string]any{"session": v, "access": access})
	}
	writeJSON(w, 200, map[string]any{"items": out, "total": total})
	return nil
}
func (a *App) sessionDetail(w http.ResponseWriter, r *http.Request) error {
	v, _, e := a.inspectSession(r, "session.read")
	if e != nil {
		return e
	}
	access, e := a.audit.Access(r.Context(), v.ID)
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"session": v, "access": access})
	return nil
}
func (a *App) terminateSession(w http.ResponseWriter, r *http.Request) error {
	v, u, e := a.inspectSession(r, "identity.manage")
	if e != nil {
		return e
	}
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		if e := a.sessions.Terminate(ctx, v.ID); e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "session.terminate_requested", requestID(r), sourceIP(r), map[string]string{"sessionId": v.ID})
	})
	if e != nil {
		return e
	}
	a.liveMu.Lock()
	live := a.live[v.ID]
	a.liveMu.Unlock()
	if live != nil {
		live.stop("terminated")
	}
	w.WriteHeader(204)
	return nil
}
func (a *App) sessionCommands(w http.ResponseWriter, r *http.Request) error {
	v, _, e := a.inspectSession(r, "audit.read")
	if e != nil {
		return e
	}
	if r.URL.Query().Has("current") {
		page, e := strconv.Atoi(r.URL.Query().Get("current"))
		if e != nil || page < 1 || page > 1000000 {
			return fault.Invalid
		}
		size := 20
		if r.URL.Query().Has("pageSize") {
			size, e = strconv.Atoi(r.URL.Query().Get("pageSize"))
			if e != nil || size < 1 || size > 100 {
				return fault.Invalid
			}
		}
		items, total, e := a.audit.CommandPage(r.Context(), v.ID, (page-1)*size, size)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"items": items, "total": total})
		return nil
	}
	items, e := a.audit.Commands(r.Context(), v.ID)
	if e != nil {
		return e
	}
	writeJSON(w, 200, items)
	return nil
}
func (a *App) finishSession(id, state, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	e := a.db.Within(ctx, func(ctx context.Context) error {
		closed, e := a.sessions.LockFinalization(ctx, id)
		if e != nil {
			return e
		}
		if closed {
			return nil
		}
		if e := a.sessions.Finish(ctx, id, state, reason); e != nil {
			return e
		}
		if _, e := a.db.Exec(ctx, `UPDATE native_forward_streams SET state='interrupted',ended_at=now() WHERE session_id=$1 AND ended_at IS NULL`, id); e != nil {
			return e
		}
		return a.audit.AccessState(ctx, id, state, reason, true)
	})
	// Finalization must not roll back if hashing a large recording exceeds its
	// budget. An unsealed recording is verified/sealed before its first replay.
	if e == nil {
		_ = a.audit.VerifyRecording(ctx, id)
	}
}

// RunSessionMaintenance must run for the lifetime of the server. Expired/crashed leases fail closed.
func (a *App) RunSessionMaintenance(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			a.liveMu.Lock()
			for _, v := range a.live {
				v.stop("server_shutdown")
			}
			a.liveMu.Unlock()
			return
		case <-tick.C:
			cleanupCtx, cleanupCancel := context.WithTimeout(ctx, time.Second)
			_ = a.files.Recover(cleanupCtx, false)
			_ = a.audit.SyncUploads(cleanupCtx)
			_, _ = a.db.Exec(cleanupCtx, `UPDATE native_bridge_reservations SET host_key=''::bytea WHERE expires_at<=now() AND octet_length(host_key)>0`)
			cleanupCancel()
			c, cancel := context.WithTimeout(ctx, time.Second)
			_ = database.New(a.db).ReapSessions(c)
			ids, e := a.sessions.Expired(c)
			cancel()
			if e == nil {
				for _, id := range ids {
					a.finishSession(id, "interrupted", "lease_expired")
				}
			}
		}
	}
}

// HTTP Shutdown does not wait for hijacked WebSockets; drain these before closing PostgreSQL.
func (a *App) StopSessions(ctx context.Context) {
	a.connectivity.StopAgents()
	a.liveMu.Lock()
	for _, v := range a.live {
		v.stop("server_shutdown")
	}
	a.liveMu.Unlock()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		a.liveMu.Lock()
		n := len(a.live)
		a.liveMu.Unlock()
		if n == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (a *App) routesR04(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("GET /api/admin/assets/{id}/host-key", a.hostKey)
	h("POST /api/admin/assets/{id}/host-key/discover", a.discoverHostKey)
	h("PUT /api/admin/assets/{id}/host-key", a.trustHostKey)
	h("POST /api/sessions", a.reserveSession)
	h("GET /api/sessions", a.listSessions)
	h("GET /api/statistics/sessions", a.sessionStatistics)
	h("GET /api/statistics/assets", a.assetStatistics)
	h("GET /api/sessions/{id}", a.sessionDetail)
	h("GET /api/sessions/{id}/ws", a.sessionWS)
	h("POST /api/sessions/{id}/terminate", a.terminateSession)
	h("GET /api/sessions/{id}/commands", a.sessionCommands)
	h("GET /api/sessions/{id}/recording", a.sessionRecording)
	h("GET /api/sessions/{id}/recording/segments/{seq}", a.recordingSegment)
	h("GET /api/sessions/{id}/recording/download", a.downloadRecording)
	h("GET /api/sessions/{id}/recording/mp4", a.recordingExportStatus)
	h("POST /api/sessions/{id}/recording/mp4", a.requestRecordingExport)
	h("DELETE /api/sessions/{id}/recording/mp4", a.cancelRecordingExport)
	h("GET /api/sessions/{id}/recording/mp4/download", a.downloadRecordingMP4)
	h("POST /api/sessions/{id}/commands", a.executeCommand)
}
