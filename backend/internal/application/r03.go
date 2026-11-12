package application

import (
	"context"
	"errors"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/authorization"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/vault"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"time"
)

func (a *App) resourceFilter(r *http.Request, portal bool) (resource.Filter, error) {
	u, e := a.authenticated(r)
	if e != nil {
		return resource.Filter{}, e
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("current"))
	size, _ := strconv.Atoi(q.Get("pageSize"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	if page > 1000000 {
		return resource.Filter{}, fault.Invalid
	}
	f := resource.Filter{Search: q.Get("search"), GroupID: q.Get("groupId"), Tag: q.Get("tag"), ID: r.PathValue("id"), Offset: (page - 1) * size, Limit: size, EnabledOnly: portal, All: !portal || u.Type == "admin"}
	if (f.GroupID != "" && !resource.ValidID(f.GroupID)) || (f.ID != "" && !resource.ValidID(f.ID)) {
		return f, fault.Invalid
	}
	if !portal {
		if e = a.allowed(r.Context(), u, "asset.read"); e != nil {
			return f, e
		}
	} else if !f.All {
		departments, e := a.identity.DepartmentIDs(r.Context(), u.ID)
		if e != nil {
			return f, e
		}
		f.Targets, e = a.authz.Targets(r.Context(), u.ID, departments)
		if e != nil {
			return f, e
		}
	}
	return f, nil
}
func (a *App) listAssets(portal bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		f, e := a.resourceFilter(r, portal)
		if e != nil {
			return e
		}
		items, total, e := a.resources.List(r.Context(), f)
		if e != nil {
			return e
		}
		if portal {
			for i := range items {
				items[i].CredentialID = ""
			}
		}
		if r.PathValue("id") != "" {
			if len(items) == 0 {
				if portal {
					return fault.Denied
				}
				return problem(404, "not_found", "资产不存在")
			}
			writeJSON(w, 200, items[0])
		} else {
			writeJSON(w, 200, map[string]any{"items": items, "total": total})
		}
		return nil
	}
}
func (a *App) saveAsset(ctx context.Context, in resource.Asset) (string, error) {
	if e := resource.Validate(in); e != nil {
		return "", e
	}
	if in.ProxyID != "" {
		p, e := a.connectivity.Proxy(ctx, in.ProxyID)
		if e != nil {
			return "", e
		}
		if !p.Enabled {
			return "", fault.Denied
		}
	}
	if in.GatewayGroupID != "" {
		g, e := a.connectivity.Group(ctx, in.GatewayGroupID)
		if e != nil {
			return "", e
		}
		if !g.Enabled {
			return "", fault.Denied
		}
	}
	if in.JumpID != "" {
		j, e := a.connectivity.Jump(ctx, in.JumpID)
		if e != nil {
			return "", e
		}
		if !j.Enabled {
			return "", fault.Denied
		}
	}
	if in.AgentID != "" {
		if e := a.connectivity.ValidateDevice(ctx, in.AgentID); e != nil {
			return "", e
		}
	}
	cred, e := a.vault.Get(ctx, in.CredentialID)
	if e != nil {
		return "", e
	}
	if (in.Protocol == "rdp" || in.Protocol == "vnc") && cred.Kind != "password" {
		return "", problem(422, "password_required", "图形资产需要密码凭据")
	}
	return a.resources.Save(ctx, in)
}
func (a *App) assetSave(w http.ResponseWriter, r *http.Request) error {
	var in resource.Asset
	if e := decode(w, r, &in); e != nil {
		return e
	}
	in.ID = r.PathValue("id")
	if e := a.prepareRDPCertificate(r, &in); e != nil {
		return e
	}
	var id string
	e := a.mutate(r, "asset.saved", func(ctx context.Context, u *identity.User) (string, error) {
		var e error
		id, e = a.saveAsset(ctx, in)
		return id, e
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]string{"id": id})
	return nil
}
func (a *App) credentials(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "credential.read"); e != nil {
		return e
	}
	items, e := a.vault.List(r.Context())
	if e != nil {
		return e
	}
	for i := range items {
		refs, e := a.resources.CredentialReferences(r.Context(), items[i].ID)
		if e != nil {
			return e
		}
		items[i].AssetCount = len(refs)
	}
	writeJSON(w, 200, items)
	return nil
}
func (a *App) credentialSave(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "identity.manage"); e != nil {
		return e
	}
	var in vault.Write
	if e := decode(w, r, &in); e != nil {
		return e
	}
	in.ID = r.PathValue("id")
	var id string
	e := a.mutate(r, "credential.saved", func(ctx context.Context, u *identity.User) (string, error) {
		var e error
		id, e = a.vault.Save(ctx, in)
		return id, e
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]string{"id": id})
	return nil
}
func (a *App) revealCredential(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "identity.manage"); e != nil {
		return e
	}
	var in struct {
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if len(in.Password) > 1024 {
		return fault.Invalid
	}
	release, e := a.hashPermit(r)
	if e != nil {
		return e
	}
	defer release()
	var secret vault.Secret
	e = a.securityTx(r, func(ctx context.Context, u *identity.User) error {
		if u.Phase != "authenticated" || u.Type != "admin" {
			return fault.Denied
		}
		v, e := a.identity.Verify(ctx, u.Username, in.Password)
		if e != nil {
			return e
		}
		if v == nil {
			return fault.Proof
		}
		enabled, _, e := a.identity.MFAStatus(ctx, u.ID)
		if e != nil {
			return e
		}
		if !enabled {
			return problem(403, "mfa_required", "请先在我的账号绑定 TOTP，再查看秘密")
		}
		if e = a.identity.ConsumeTOTP(ctx, u.ID, in.TOTP); e != nil {
			return e
		}
		secret, e = a.vault.Reveal(ctx, r.PathValue("id"), u.ID, requestID(r))
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "credential.revealed", requestID(r), sourceIP(r), map[string]string{"target": r.PathValue("id")})
	})
	if e != nil {
		return e
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, secret)
	return nil
}
func (a *App) grantSave(w http.ResponseWriter, r *http.Request) error {
	var in authorization.Grant
	if e := decode(w, r, &in); e != nil {
		return e
	}
	in.ID = r.PathValue("id")
	var id string
	e := a.mutate(r, "grant.saved", func(ctx context.Context, u *identity.User) (string, error) {
		if in.ApprovalID != "" {
			return "", fault.Invalid
		}
		if !resource.ValidID(in.ResourceID) || ((in.UserID == "") == (in.DepartmentID == "")) {
			return "", fault.Invalid
		}
		exists, e := a.identity.SubjectExists(ctx, in.UserID, in.DepartmentID)
		if e != nil {
			return "", e
		}
		if !exists {
			return "", fault.Invalid
		}
		exists, e = a.resources.Exists(ctx, in.ResourceID)
		if e != nil {
			return "", e
		}
		if !exists {
			return "", fault.Invalid
		}
		id, e = a.authz.SaveGrant(ctx, in)
		return id, e
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]string{"id": id})
	return nil
}
func (a *App) importAssets(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "identity.manage"); e != nil {
		return e
	}
	var in struct {
		Items []resource.Asset `json:"items"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if len(in.Items) == 0 || len(in.Items) > 100 {
		return problem(422, "import_limit", "每批需包含1至100行资产")
	}
	results := []map[string]any{}
	success := 0
	for i, v := range in.Items {
		var id string
		e := a.mutate(r, "asset.imported", func(ctx context.Context, u *identity.User) (string, error) {
			if v.ID != "" {
				return "", fault.Invalid
			}
			var e error
			id, e = a.saveAsset(ctx, v)
			return id, e
		})
		row := map[string]any{"row": i + 1, "success": e == nil}
		if e != nil {
			message := "名称重复、引用不可用或字段无效；本行未写入"
			if errors.Is(e, fault.Denied) {
				message = "权限已撤销，本行未写入"
			}
			row["message"] = message
		} else {
			row["id"] = id
			success++
		}
		results = append(results, row)
	}
	writeJSON(w, 200, map[string]any{"results": results, "succeeded": success, "failed": len(in.Items) - success})
	return nil
}
func (a *App) exportAssets(w http.ResponseWriter, r *http.Request) error {
	f, e := a.resourceFilter(r, false)
	if e != nil {
		return e
	}
	f.Offset = 0
	f.Limit = 10000
	items, total, e := a.resources.List(r.Context(), f)
	if e != nil {
		return e
	}
	if total > 10000 {
		return problem(422, "export_limit", "请用过滤条件把导出范围缩小到10000条以内")
	}
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	if e = a.audit.Detail(r.Context(), u.ID, "asset.exported", requestID(r), sourceIP(r), map[string]string{"count": strconv.Itoa(total)}); e != nil {
		return e
	}
	for i := range items {
		items[i].ID = ""
		items[i].Version = 0
	}
	w.Header().Set("Content-Disposition", `attachment; filename="assets-r03.json"`)
	writeJSON(w, 200, map[string]any{"items": items})
	return nil
}

// Probe only opens a TCP socket to a saved asset; no credentials or SSH authentication are used.
func (a *App) probeAsset(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "identity.manage"); e != nil {
		return e
	}
	if !a.allowAttempt("probe:" + sourceIP(r)) {
		return problem(429, "rate_limited", "连通检查过于频繁")
	}
	select {
	case a.probeSlots <- struct{}{}:
		defer func() { <-a.probeSlots }()
	default:
		return problem(429, "busy", "连通检查繁忙")
	}
	items, _, e := a.resources.List(r.Context(), resource.Filter{All: true, ID: r.PathValue("id"), Limit: 1})
	if e != nil {
		return e
	}
	if len(items) == 0 {
		return problem(404, "not_found", "资产不存在")
	}
	target := items[0]
	if !target.Enabled {
		return fault.Conflict
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	started := time.Now()
	addresses, e := net.DefaultResolver.LookupIPAddr(ctx, target.Host)
	reachable := false
	message := "解析失败或目标不可达"
	var selected net.IP
	if e == nil {
		for _, addr := range addresses {
			ip := addr.IP
			if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				return problem(422, "blocked_target", "禁止检查未指定、多播或链路本地地址")
			}
			if selected == nil {
				selected = ip
			}
		}
		if selected != nil {
			dialer := net.Dialer{}
			conn, e := dialer.DialContext(ctx, "tcp", net.JoinHostPort(selected.String(), strconv.Itoa(target.Port)))
			if e == nil {
				_ = conn.Close()
				reachable = true
				message = "TCP 可达；尚未验证 SSH 服务或凭据"
			}
		}
	}
	u, e := a.require(r, "identity.manage")
	if e != nil {
		return e
	}
	if e = a.audit.Detail(r.Context(), u.ID, "asset.tcp_probe", requestID(r), sourceIP(r), map[string]string{"target": target.ID, "reachable": strconv.FormatBool(reachable)}); e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"reachable": reachable, "kind": "tcp", "durationMs": time.Since(started).Milliseconds(), "message": message})
	return nil
}
func (a *App) routesR03(handle func(string, func(http.ResponseWriter, *http.Request) error)) {
	handle("GET /api/admin/assets/paging", a.listAssets(false))
	handle("GET /api/admin/assets/{id}", a.listAssets(false))
	handle("POST /api/admin/assets", a.assetSave)
	handle("PUT /api/admin/assets/{id}", a.assetSave)
	handle("GET /api/portal/assets", a.listAssets(true))
	handle("GET /api/portal/assets/{id}", a.listAssets(true))
	handle("GET /api/portal/facets", func(w http.ResponseWriter, r *http.Request) error {
		f, e := a.resourceFilter(r, true)
		if e != nil {
			return e
		}
		v, e := a.resources.Facets(r.Context(), f)
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})

	handle("POST /api/admin/assets/import", a.importAssets)
	handle("GET /api/admin/assets/export", a.exportAssets)
	handle("POST /api/admin/assets/{id}/probe", a.probeAsset)
	handle("POST /api/admin/rdp/certificate", a.rdpCertificate)
	handle("GET /api/admin/resource-groups", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "asset.read"); e != nil {
			return e
		}
		v, e := a.resources.Groups(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	saveGroup := func(w http.ResponseWriter, r *http.Request) error {
		var in resource.Group
		if e := decode(w, r, &in); e != nil {
			return e
		}
		in.ID = r.PathValue("id")
		var id string
		e := a.mutate(r, "resource_group.saved", func(ctx context.Context, u *identity.User) (string, error) {
			var e error
			id, e = a.resources.SaveGroup(ctx, in)
			return id, e
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return nil
	}
	handle("POST /api/admin/resource-groups", saveGroup)
	handle("PUT /api/admin/resource-groups/{id}", saveGroup)
	handle("GET /api/admin/credentials", a.credentials)
	handle("POST /api/admin/credentials", a.credentialSave)
	handle("PUT /api/admin/credentials/{id}", a.credentialSave)
	handle("POST /api/admin/credentials/{id}/reveal", a.revealCredential)
	handle("GET /api/admin/credentials/{id}/references", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		v, e := a.resources.CredentialReferences(r.Context(), r.PathValue("id"))
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"assetNames": v})
		return nil
	})
	handle("DELETE /api/admin/credentials/{id}", func(w http.ResponseWriter, r *http.Request) error {
		e := a.mutate(r, "credential.deleted", func(ctx context.Context, u *identity.User) (string, error) {
			id := r.PathValue("id")
			refs, e := a.resources.CredentialReferences(ctx, id)
			if e != nil {
				return "", e
			}
			if len(refs) > 0 {
				return "", problem(409, "credential_referenced", "凭据仍被资产引用，请先调整引用")
			}
			return id, a.vault.Delete(ctx, id)
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	handle("GET /api/admin/grants", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "grant.read"); e != nil {
			return e
		}
		v, e := a.authz.Grants(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("POST /api/admin/grants", a.grantSave)
	handle("PUT /api/admin/grants/{id}", a.grantSave)
	handle("DELETE /api/admin/grants/{id}", func(w http.ResponseWriter, r *http.Request) error {
		e := a.mutate(r, "grant.revoked", func(ctx context.Context, u *identity.User) (string, error) {
			id := r.PathValue("id")
			return id, a.authz.RevokeGrant(ctx, id)
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	for _, entry := range []struct{ path, kind string }{{"assets", "asset"}, {"resource-groups", "group"}} {
		handle("DELETE /api/admin/"+entry.path+"/{id}", func(w http.ResponseWriter, r *http.Request) error {
			e := a.mutate(r, entry.kind+".deleted", func(ctx context.Context, u *identity.User) (string, error) {
				id := r.PathValue("id")
				return id, a.resources.Delete(ctx, id, entry.kind)
			})
			if e != nil {
				return e
			}
			w.WriteHeader(204)
			return nil
		})
	}
}
