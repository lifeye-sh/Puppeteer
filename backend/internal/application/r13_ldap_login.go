package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"time"
)

func (a *App) ldapLoginProviders(w http.ResponseWriter, r *http.Request) error {
	all, e := a.identity.LDAPProviders(r.Context())
	if e != nil {
		return e
	}
	out := []map[string]string{}
	for _, p := range all {
		if p.Enabled {
			out = append(out, map[string]string{"id": p.ID, "name": p.Name})
		}
	}
	writeJSON(w, 200, map[string]any{"items": out})
	return nil
}
func (a *App) ldapBindings(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	items, e := a.identity.LDAPIdentities(r.Context(), u.ID)
	if e != nil {
		return e
	}
	all, e := a.identity.LDAPProviders(r.Context())
	if e != nil {
		return e
	}
	out := []map[string]string{}
	for _, p := range all {
		if p.BindingEnabled {
			out = append(out, map[string]string{"id": p.ID, "name": p.Name})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "providers": out})
	return nil
}
func (a *App) bindLDAP(w http.ResponseWriter, r *http.Request) error {
	if !resource.ValidID(r.PathValue("id")) {
		return fault.Invalid
	}
	var in struct {
		backupProof
		Username          string `json:"username"`
		DirectoryPassword string `json:"directoryPassword"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if in.Username == "" || in.DirectoryPassword == "" {
		return fault.Invalid
	}
	var user string
	var version int64
	e := a.withPasswordProof(r, in.backupProof, "", func(ctx context.Context, u *identity.User) error {
		user = u.ID
		var e error
		version, e = a.identity.UserVersion(ctx, u.ID)
		return e
	})
	if e != nil {
		return e
	}
	p, e := a.identity.LDAPProvider(r.Context(), r.PathValue("id"))
	if e != nil || !p.BindingEnabled {
		return fault.Proof
	}
	secret, e := a.identity.LDAPPassword(r.Context(), p.ID, a.config.VaultKey)
	if e != nil {
		return e
	}
	subject, e := identity.VerifyLDAP(r.Context(), p, secret, in.Username, in.DirectoryPassword)
	if e != nil {
		return fault.Proof
	}
	e = a.securityTx(r, func(ctx context.Context, u *identity.User) error {
		if u.ID != user {
			return fault.Proof
		}
		if e := a.identity.BindLDAPIdentity(ctx, user, p, subject, version); e != nil {
			return e
		}
		return a.audit.Detail(ctx, user, "identity.ldap_bound", requestID(r), sourceIP(r), map[string]string{"providerId": p.ID})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *App) unbindLDAP(w http.ResponseWriter, r *http.Request) error {
	if !resource.ValidID(r.PathValue("id")) {
		return fault.Invalid
	}
	var in backupProof
	if e := decode(w, r, &in); e != nil {
		return e
	}
	e := a.withPasswordProof(r, in, "", func(ctx context.Context, u *identity.User) error {
		if e := a.identity.UnbindLDAPIdentity(ctx, u.ID, r.PathValue("id")); e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "identity.ldap_unbound", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("id")})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *App) ldapLogin(w http.ResponseWriter, r *http.Request) error {
	started := time.Now()
	if !strings.HasPrefix(r.Header.Get("Origin"), "https://") {
		return fault.Proof
	}
	var in struct {
		Source     string `json:"source"`
		ProviderID string `json:"providerId"`
		Username   string `json:"username"`
		Password   string `json:"password"`
		Key        string `json:"key"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if in.Source != "ldap" || !resource.ValidID(in.ProviderID) || in.Username == "" || in.Password == "" || len(in.Username) > 256 || len(in.Password) > 1024 {
		return fault.Invalid
	}
	release, e := a.hashPermit(r)
	if e != nil {
		return e
	}
	defer release()
	if e = a.checkLoginCaptcha(r, in.Key); e != nil {
		return e
	}
	state, e := a.system.State(r.Context())
	if e != nil {
		return e
	}
	if !state.Initialized {
		return fault.Proof
	}
	lockName := "ldap:" + identity.Digest(in.ProviderID+":"+strings.ToLower(in.Username))
	if e = a.db.Within(r.Context(), func(ctx context.Context) error { return a.identity.CheckLock(ctx, lockName, sourceIP(r)) }); e != nil {
		return fault.Proof
	}
	fail := func() error {
		e := a.db.Within(r.Context(), func(ctx context.Context) error {
			if e := a.identity.FailedPassword(ctx, lockName, sourceIP(r)); e != nil {
				return e
			}
			return a.audit.Login(ctx, "", "ldap_failed", requestID(r), sourceIP(r))
		})
		if e != nil {
			return e
		}
		return fault.Proof
	}
	p, e := a.identity.LDAPProvider(r.Context(), in.ProviderID)
	if e != nil || !p.Enabled {
		return fail()
	}
	secret, e := a.identity.LDAPPassword(r.Context(), p.ID, a.config.VaultKey)
	if e != nil {
		return fail()
	}
	subject, e := identity.VerifyLDAP(r.Context(), p, secret, in.Username, in.Password)
	if e != nil {
		return fail()
	}
	var session, phase string
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		if e := a.identity.CheckLock(ctx, lockName, sourceIP(r)); e != nil {
			return e
		}
		u, e := a.identity.LDAPLoginUser(ctx, p, subject, started)
		if e != nil {
			return e
		}
		current, e := a.system.State(ctx)
		if e != nil {
			return e
		}
		if !current.Initialized || current.AuthEpoch != state.AuthEpoch {
			return fault.Proof
		}
		enabled, required, e := a.identity.MFAStatus(ctx, u.ID)
		if e != nil {
			return e
		}
		phase = "authenticated"
		if enabled {
			phase = "mfa_pending"
		} else if required {
			phase = "mfa_enroll"
		}
		u.Phase = phase
		if e = a.identity.Revoke(ctx, token(r)); e != nil {
			return e
		}
		session, e = a.identity.Issue(ctx, u, state.AuthEpoch, sourceIP(r))
		if e != nil {
			return e
		}
		if e = a.identity.Unlock(ctx, lockName); e != nil {
			return e
		}
		return a.audit.Login(ctx, u.ID, "ldap_"+phase, requestID(r), sourceIP(r))
	})
	if e != nil {
		return fail()
	}
	if err := a.setSession(w, r, session, true); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"needTotp": phase == "mfa_pending", "needRebind": phase == "mfa_enroll"})
	return nil
}
