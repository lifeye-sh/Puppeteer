package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
)

func (a *App) oidcProviders(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "identity.manage"); e != nil {
		return e
	}
	items, e := a.identity.OIDCProviders(r.Context())
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"items": items})
	return nil
}
func (a *App) testOIDCProvider(w http.ResponseWriter, r *http.Request) error {
	u, e := a.require(r, "identity.manage")
	if e != nil {
		return e
	}
	if !resource.ValidID(r.PathValue("id")) {
		return fault.Invalid
	}
	if !a.allowAttempt("oidc-discovery:" + u.ID) {
		return problem(429, "rate_limited", "检测过于频繁，请稍后重试")
	}
	v, e := a.identity.OIDCProvider(r.Context(), r.PathValue("id"))
	if e != nil {
		return e
	}
	if v.Kind == "wecom" {
		secret, e := a.identity.OIDCClientSecret(r.Context(), v.ID, a.config.VaultKey)
		if e != nil {
			return e
		}
		p, e := identity.NewWeComProtocol(v, secret, a.oidcHTTPClient)
		if e != nil {
			return e
		}
		checkErr := p.Check(r.Context())
		status := "succeeded"
		if checkErr != nil {
			status = "failed"
		}
		if e = a.audit.Detail(r.Context(), u.ID, "identity.wecom_provider_tested", requestID(r), sourceIP(r), map[string]string{"id": v.ID, "status": status}); e != nil {
			return e
		}
		if checkErr != nil {
			return problem(422, "wecom_unavailable", "企业微信凭据检测失败，请检查证书、企业 ID 和应用 Secret")
		}
		writeJSON(w, 200, map[string]any{"version": v.Version, "message": "企业微信应用凭据验证通过；用户授权请使用验证授权功能"})
		return nil
	}
	metadata, err := identity.DiscoverOIDC(r.Context(), v.Issuer, a.oidcHTTPClient)
	status := "succeeded"
	if err != nil {
		status = "failed"
	}
	if e = a.audit.Detail(r.Context(), u.ID, "identity.oidc_provider_tested", requestID(r), sourceIP(r), map[string]string{"id": v.ID, "status": status}); e != nil {
		return e
	}
	if err != nil {
		return problem(422, "oidc_discovery_failed", "元数据检测失败：请检查 HTTPS 证书、Issuer 精确匹配，以及 code、RS256、S256 支持")
	}
	writeJSON(w, 200, map[string]any{"metadata": metadata, "version": v.Version, "message": "元数据检测通过；不代表客户端凭据或登录回调已验证"})
	return nil
}
func (a *App) saveOIDCProvider(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		backupProof
		identity.OIDCProvider
		ClientSecret string `json:"clientSecret"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	in.ID = r.PathValue("id")
	if in.ID != "" && !resource.ValidID(in.ID) {
		return fault.Invalid
	}
	var id string
	e := a.withPasswordProof(r, in.backupProof, "identity.manage", func(c context.Context, u *identity.User) error {
		var e error
		id, e = a.identity.SaveOIDCProvider(c, in.OIDCProvider, in.ClientSecret)
		if e != nil {
			return e
		}
		return a.audit.Detail(c, u.ID, "identity.oidc_provider_saved", requestID(r), sourceIP(r), map[string]string{"id": id})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]string{"id": id})
	return nil
}
