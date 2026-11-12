package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"strings"
)

func (a *App) oidcProtocol(ctx context.Context, p identity.OIDCProvider) (identity.FederationProtocol, error) {
	secret, err := a.identity.OIDCClientSecret(ctx, p.ID, a.config.VaultKey)
	if err != nil {
		return nil, err
	}
	if p.Kind == "wecom" {
		return identity.NewWeComProtocol(p, secret, a.oidcHTTPClient)
	}
	protocol, err := identity.NewOIDCProtocol(ctx, p, secret, a.oidcHTTPClient)
	if err != nil {
		return nil, problem(422, "oidc_unavailable", "身份提供方不可用，请检查证书和元数据配置")
	}
	return protocol, nil
}

func (a *App) startOIDCAuthorizationTest(w http.ResponseWriter, r *http.Request) error {
	return a.startOIDCAuthorization(w, r, "test")
}
func (a *App) startOIDCAuthorization(w http.ResponseWriter, r *http.Request, purpose string) error {
	permission := ""
	if purpose == "test" {
		permission = "identity.manage"
	}
	if _, err := a.authenticated(r); err != nil {
		return err
	}
	if !resource.ValidID(r.PathValue("id")) {
		return fault.Invalid
	}
	var in backupProof
	if err := decode(w, r, &in); err != nil {
		return err
	}
	var authorization string
	err := a.withPasswordProof(r, in, permission, func(ctx context.Context, u *identity.User) error {
		p, err := a.identity.OIDCProvider(ctx, r.PathValue("id"))
		if err != nil {
			return err
		}
		if purpose == "bind" && !p.BindingEnabled {
			return fault.Denied
		}
		if !strings.HasPrefix(r.Header.Get("Origin"), "https://") || p.RedirectURI != r.Header.Get("Origin")+p.CallbackPath() {
			return problem(422, "oidc_callback", "回调地址必须匹配当前平台 HTTPS 地址及所选身份提供方的回调路径")
		}
		protocol, err := a.oidcProtocol(ctx, p)
		if err != nil {
			return err
		}
		var challenge identity.OIDCAuthorization
		authorization, challenge = protocol.Begin()
		if err = a.identity.SaveOIDCAuthorization(ctx, u.ID, token(r), purpose, p, challenge); err != nil {
			return err
		}
		return a.audit.Detail(ctx, u.ID, "identity.oidc_authorization_started", requestID(r), sourceIP(r), map[string]string{"id": p.ID, "purpose": purpose})
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]string{"authorizationUrl": authorization})
	return nil
}

func (a *App) finishOIDCAuthorizationTest(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
	if query := r.URL.Query(); len(query["state"]) == 1 {
		login, err := a.identity.HasOIDCLogin(r.Context(), query.Get("state"))
		if err != nil {
			return err
		}
		if login {
			return a.finishOIDCLogin(w, r)
		}
	}
	u, err := a.authenticated(r)
	if err != nil {
		return err
	}
	query := r.URL.Query()
	if len(query["state"]) != 1 {
		return fault.Proof
	}
	purpose, err := a.identity.OIDCAuthorizationPurpose(r.Context(), query.Get("state"), token(r), u.ID)
	if err != nil {
		return err
	}
	if purpose == "test" {
		if err = a.allowed(r.Context(), u, "identity.manage"); err != nil {
			return err
		}
	}
	// Consumption precedes all network or signature verification, and cannot roll back.
	stored, challenge, err := a.identity.ConsumeOIDCAuthorization(r.Context(), query.Get("state"), token(r), u.ID, purpose)
	if err != nil {
		return err
	}
	p, err := a.identity.OIDCProvider(r.Context(), stored.ID)
	if err != nil {
		return err
	}
	if p.Version != stored.Version {
		return fault.Proof
	}
	if r.URL.Path != p.CallbackPath() {
		return fault.Proof
	}
	protocol, exchangeErr := a.oidcProtocol(r.Context(), p)
	var subject identity.OIDCSubject
	if exchangeErr == nil {
		subject, exchangeErr = protocol.Exchange(r.Context(), challenge, query)
	}
	status := "succeeded"
	if exchangeErr != nil {
		status = "failed"
	}
	err = a.securityTx(r, func(ctx context.Context, current *identity.User) error {
		if current.Phase != "authenticated" {
			return fault.Denied
		}
		if purpose == "test" {
			if err := a.allowed(ctx, current, "identity.manage"); err != nil {
				return err
			}
		}
		latest, err := a.identity.OIDCProvider(ctx, p.ID)
		if err != nil {
			return err
		}
		if latest.Version != stored.Version {
			return fault.Proof
		}
		if purpose == "bind" && exchangeErr == nil {
			if err := a.identity.BindExternalIdentity(ctx, current.ID, latest, subject, challenge.UserVersion); err != nil {
				return err
			}
			return a.audit.Detail(ctx, current.ID, "identity.external_bound", requestID(r), sourceIP(r), map[string]string{"providerId": p.ID})
		}
		return a.audit.Detail(ctx, current.ID, "identity.oidc_authorization_tested", requestID(r), sourceIP(r), map[string]string{"id": p.ID, "status": status})
	})
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if exchangeErr != nil {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>授权验证失败</title><h1>授权验证失败</h1><p>请检查客户端密钥、回调地址和提供方配置，然后重新发起验证。此次授权已失效。</p><a href="/setting">返回配置页</a></html>`))
	} else {
		if purpose == "bind" {
			_, _ = w.Write([]byte(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>绑定成功</title><h1>外部账号绑定成功</h1><p>身份已绑定至当前平台账号。登录是否开放由管理员配置。</p><a href="/info">返回个人中心</a></html>`))
			return nil
		}
		_, _ = w.Write([]byte(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>授权验证通过</title><h1>授权验证通过</h1><p>授权码交换及身份令牌验证成功。本次仅测试连接，未绑定外部账号，也未启用联合登录。</p><a href="/setting">返回配置页</a></html>`))
	}
	return nil
}
