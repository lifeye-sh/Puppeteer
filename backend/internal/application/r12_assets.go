package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/database"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
)

func (a *App) databaseAssets(portal bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		all := u.Type == "admin" || !portal
		var targets []string
		if !portal {
			if e = a.allowed(r.Context(), u, "database.read"); e != nil {
				return e
			}
		} else if !all {
			departments, e := a.identity.DepartmentIDs(r.Context(), u.ID)
			if e != nil {
				return e
			}
			targets, e = a.authz.Targets(r.Context(), u.ID, departments)
			if e != nil {
				return e
			}
		}
		list, e := database.New(a.db).List(r.Context(), all, targets)
		if e != nil {
			return e
		}
		if portal {
			security, e := a.system.SecurityConfiguration(r.Context())
			if e != nil {
				return e
			}
			canWrite := !security.Values.Bool(system.DatabaseBlockDMLKey) && a.allowed(r.Context(), u, "database.write") == nil
			for i := range list {
				list[i].NativeWriteAvailable = canWrite && list[i].Engine == "mysql" && list[i].NativeWriteCredentialID != ""
				list[i].ReadonlyCredentialID = ""
				list[i].WriteCredentialID = ""
				list[i].NativeWriteCredentialID = ""
				list[i].TLSCA = ""
			}
		}
		writeJSON(w, 200, map[string]any{"items": list, "total": len(list)})
		return nil
	}
}
func (a *App) saveDatabaseAsset(w http.ResponseWriter, r *http.Request) error {
	var in database.Asset
	if e := decode(w, r, &in); e != nil {
		return e
	}
	in.ID = r.PathValue("id")
	if in.ID != "" && !resource.ValidID(in.ID) {
		return fault.Invalid
	}
	var id string
	e := a.mutate(r, "database.asset_saved", func(c context.Context, u *identity.User) (string, error) {
		if in.PermissionMode == "" {
			in.PermissionMode = "account"
			if in.ID != "" {
				current, err := database.New(a.db).Asset(c, in.ID)
				if err != nil {
					return "", err
				}
				in.PermissionMode = current.PermissionMode
			}
		}
		if e := database.Validate(in); e != nil {
			return "", e
		}
		for _, credential := range []string{in.ReadonlyCredentialID, in.WriteCredentialID, in.NativeWriteCredentialID} {
			if credential == "" {
				continue
			}
			v, e := a.vault.Get(c, credential)
			if e != nil {
				return "", e
			}
			if v.Kind != "password" {
				return "", fault.Invalid
			}
		}
		var e error
		id, e = database.New(a.db).Save(c, in)
		if e == nil {
			mode := in.TLSMode
			if mode == "" {
				mode = "verify-full"
			}
			e = a.audit.Detail(c, u.ID, "database.transport_saved", requestID(r), sourceIP(r), map[string]string{"target": id, "tlsMode": mode, "permissionMode": in.PermissionMode})
		}
		return id, e
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]string{"id": id})
	return nil
}
func (a *App) deleteDatabaseAsset(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	version, e := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
	if e != nil || version < 1 || !resource.ValidID(id) {
		return fault.Invalid
	}
	e = a.mutate(r, "database.asset_deleted", func(c context.Context, u *identity.User) (string, error) {
		return id, database.New(a.db).Delete(c, id, version)
	})
	if e != nil {
		return e
	}
	w.WriteHeader(204)
	return nil
}
