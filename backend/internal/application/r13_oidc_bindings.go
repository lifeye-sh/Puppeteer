package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
)

func (a *App) listOIDCBindings(w http.ResponseWriter, r *http.Request) error {
	u, err := a.authenticated(r)
	if err != nil {
		return err
	}
	items, err := a.identity.ExternalIdentities(r.Context(), u.ID)
	if err != nil {
		return err
	}
	all, err := a.identity.OIDCProviders(r.Context())
	if err != nil {
		return err
	}
	providers := []map[string]string{}
	for _, p := range all {
		if p.BindingEnabled {
			providers = append(providers, map[string]string{"id": p.ID, "name": p.Name, "issuer": p.Issuer})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "providers": providers})
	return nil
}

func (a *App) unbindOIDCIdentity(w http.ResponseWriter, r *http.Request) error {
	if !resource.ValidID(r.PathValue("id")) {
		return fault.Invalid
	}
	var in backupProof
	if err := decode(w, r, &in); err != nil {
		return err
	}
	err := a.withPasswordProof(r, in, "", func(ctx context.Context, u *identity.User) error {
		if err := a.identity.UnbindExternalIdentity(ctx, u.ID, r.PathValue("id")); err != nil {
			return err
		}
		return a.audit.Detail(ctx, u.ID, "identity.external_unbound", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("id")})
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

func (a *App) startOIDCBinding(w http.ResponseWriter, r *http.Request) error {
	return a.startOIDCAuthorization(w, r, "bind")
}
