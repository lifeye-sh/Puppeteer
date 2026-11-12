package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
	"strings"
)

func (a *App) databasePasswordStatus(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	v, e := a.identity.DatabasePasswordStatus(r.Context(), u.ID)
	if e != nil {
		return e
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, v)
	return nil
}
func (a *App) changeDatabasePassword(revoke bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			backupProof
			Days int `json:"days"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		if !revoke && (in.Days < 1 || in.Days > 365) {
			return fault.Invalid
		}
		var plaintext string
		e := a.withPasswordProof(r, in.backupProof, "", func(c context.Context, u *identity.User) error {
			if strings.Contains(u.Username, "@") {
				return problem(409, "database_username", "数据库代理用户名不能包含 @")
			}
			action := "database.password_revoked"
			if revoke {
				if e := a.identity.RevokeDatabasePassword(c, u.ID); e != nil {
					return e
				}
			} else {
				password, verifier, e := a.databasePasswords.Create(c)
				if e != nil {
					return e
				}
				if e = a.vault.StoreDatabaseVerifier(c, u.ID, verifier, in.Days); e != nil {
					return e
				}
				plaintext = password
				action = "database.password_rotated"
			}
			return a.audit.Detail(c, u.ID, action, requestID(r), sourceIP(r), nil)
		})
		if e != nil {
			return e
		}
		w.Header().Set("Cache-Control", "no-store")
		if revoke {
			w.WriteHeader(204)
		} else {
			writeJSON(w, 200, map[string]string{"password": plaintext})
		}
		return nil
	}
}
