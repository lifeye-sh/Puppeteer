package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
)

func (a *App) personalCredentials(kind string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		user, e := a.authenticated(r)
		if e != nil {
			return e
		}
		items, e := a.identity.PersonalCredentials(r.Context(), user.ID, kind)
		if e != nil {
			return e
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"items": items})
		return nil
	}
}
func (a *App) changePersonalCredential(kind string, revoke bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var input struct {
			backupProof
			Name      string   `json:"name"`
			PublicKey string   `json:"publicKey"`
			Scopes    []string `json:"scopes"`
			Days      int      `json:"days"`
		}
		if e := decode(w, r, &input); e != nil {
			return e
		}
		id := r.PathValue("id")
		if revoke && !resource.ValidID(id) {
			return fault.Invalid
		}
		var secret string
		e := a.withPasswordProof(r, input.backupProof, "", func(c context.Context, u *identity.User) error {
			action := "created"
			var e error
			if revoke {
				action = "revoked"
				e = a.identity.RevokePersonalCredential(c, u.ID, kind, id)
			} else {
				id, secret, e = a.identity.CreatePersonalCredential(c, u.ID, kind, input.Name, input.PublicKey, input.Scopes, input.Days)
			}
			if e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "account."+kind+"_key_"+action, requestID(r), sourceIP(r), map[string]string{"credentialId": id})
		})
		if e != nil {
			return e
		}
		w.Header().Set("Cache-Control", "no-store")
		if revoke {
			w.WriteHeader(204)
		} else {
			writeJSON(w, 201, map[string]string{"id": id, "secret": secret})
		}
		return nil
	}
}
func (a *App) apiKeyIdentity(r *http.Request, secret string) (*identity.User, error) {
	scope := ""
	if r.Method == http.MethodGet {
		scope = map[string]string{"/api/account/info": "profile.read", "/api/portal/database-assets": "database.read"}[r.URL.Path]
	}
	if scope == "" {
		return nil, fault.Denied
	}
	u, id, e := a.identity.CurrentAPIKey(r.Context(), secret, scope)
	if e != nil {
		return nil, e
	}
	if u == nil {
		return nil, problem(401, "api_key_invalid", "API Key 无效、过期、已撤销或不具备此访问范围")
	}
	if e = a.audit.Detail(r.Context(), u.ID, "account.api_key_used", requestID(r), sourceIP(r), map[string]string{"credentialId": id, "path": r.URL.Path}); e != nil {
		return nil, e
	}
	return u, nil
}
