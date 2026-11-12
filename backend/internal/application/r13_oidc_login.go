package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"strings"
)

const oidcLoginCookie = "__Host-nt_oidc_login"

func (a *App) oidcLoginProviders(w http.ResponseWriter, r *http.Request) error {
	all, err := a.identity.OIDCProviders(r.Context())
	if err != nil {
		return err
	}
	items := []map[string]string{}
	for _, p := range all {
		if p.Enabled {
			items = append(items, map[string]string{"id": p.ID, "name": p.Name})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
	return nil
}

func (a *App) startOIDCLogin(w http.ResponseWriter, r *http.Request) error {
	if !resource.ValidID(r.PathValue("id")) {
		return fault.Invalid
	}
	release, err := a.hashPermit(r)
	if err != nil {
		return err
	}
	defer release()
	var in struct {
		Key string `json:"key"`
	}
	if err = decode(w, r, &in); err != nil {
		return err
	}
	if err = a.checkLoginCaptcha(r, in.Key); err != nil {
		return err
	}
	p, err := a.identity.OIDCProvider(r.Context(), r.PathValue("id"))
	if err != nil {
		return fault.Proof
	}
	if !p.Enabled || !strings.HasPrefix(r.Header.Get("Origin"), "https://") || p.RedirectURI != r.Header.Get("Origin")+p.CallbackPath() {
		return fault.Proof
	}
	protocol, err := a.oidcProtocol(r.Context(), p)
	if err != nil {
		return err
	}
	authorization, challenge := protocol.Begin()
	binding := identity.NewToken()
	// Reuse only the opaque browser cookie, never a platform login token.
	if cookie, e := r.Cookie(oidcLoginCookie); e == nil && len(cookie.Value) == 64 {
		binding = cookie.Value
	}
	if err = a.identity.SaveOIDCLogin(r.Context(), binding, p, challenge); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: oidcLoginCookie, Value: binding, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
	writeJSON(w, 200, map[string]string{"authorizationUrl": authorization})
	return nil
}

func (a *App) finishOIDCLogin(w http.ResponseWriter, r *http.Request) error {
	err := a.exchangeOIDCLogin(w, r)
	if err != nil {
		if auditErr := a.audit.Login(r.Context(), "", "oidc_failed", requestID(r), sourceIP(r)); auditErr != nil {
			return auditErr
		}
		http.Redirect(w, r, "/login?oidcError=1", http.StatusSeeOther)
	}
	return nil
}

func (a *App) exchangeOIDCLogin(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	if len(query["state"]) != 1 {
		return fault.Proof
	}
	cookie, err := r.Cookie(oidcLoginCookie)
	if err != nil {
		return fault.Proof
	}
	c, err := a.identity.ConsumeOIDCLogin(r.Context(), query.Get("state"), cookie.Value)
	if err != nil {
		return err
	}
	p, err := a.identity.OIDCProvider(r.Context(), c.ProviderID)
	if err != nil {
		return err
	}
	if !p.Enabled || p.Version != c.ProviderVersion {
		return fault.Proof
	}
	if r.URL.Path != p.CallbackPath() {
		return fault.Proof
	}
	protocol, err := a.oidcProtocol(r.Context(), p)
	if err != nil {
		return err
	}
	subject, err := protocol.Exchange(r.Context(), c.Authorization, query)
	if err != nil {
		return fault.Proof
	}
	var session string
	phase := "authenticated"
	err = a.db.Within(r.Context(), func(ctx context.Context) error {
		u, err := a.identity.OIDCLoginUser(ctx, c, subject)
		if err != nil {
			return err
		}
		if err = a.identity.CheckLock(ctx, u.Username); err != nil {
			return err
		}
		enabled, required, err := a.identity.MFAStatus(ctx, u.ID)
		if err != nil {
			return err
		}
		if enabled {
			phase = "mfa_pending"
		} else if required {
			phase = "mfa_enroll"
		}
		state, err := a.system.State(ctx)
		if err != nil {
			return err
		}
		if !state.Initialized || state.AuthEpoch != c.AuthEpoch {
			return fault.Proof
		}
		u.Phase = phase
		if err = a.identity.Revoke(ctx, token(r)); err != nil {
			return err
		}
		session, err = a.identity.Issue(ctx, u, state.AuthEpoch, sourceIP(r))
		if err != nil {
			return err
		}
		return a.audit.Login(ctx, u.ID, "oidc_"+phase, requestID(r), sourceIP(r))
	})
	if err != nil {
		return err
	}
	if err := a.setSession(w, r, session, true); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: oidcLoginCookie, Value: "", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
	return nil
}
