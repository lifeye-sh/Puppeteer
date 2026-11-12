package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
)

func (a *App) personalCertificates(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	items, e := a.certificates().Personal(r.Context(), u.ID)
	if e != nil {
		return e
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"items": items})
	return nil
}

func (a *App) personalCertificateAction(export bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		id := r.PathValue("id")
		if !resource.ValidID(id) {
			return fault.Invalid
		}
		var in struct {
			backupProof
			ExportPassword string `json:"exportPassword"`
			Version        int64  `json:"version"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		var data []byte
		defer func() { clear(data) }()
		e := a.withPasswordProof(r, in.backupProof, "", func(ctx context.Context, u *identity.User) error {
			if u.Phase != "authenticated" {
				return fault.Denied
			}
			v, e := a.certificates().OwnedClient(ctx, u.ID, id)
			if e != nil {
				return e
			}
			if v.Version != in.Version {
				return fault.Conflict
			}
			action := "certificate.personal_revoked"
			if export {
				enabled, _, e := a.identity.MFAStatus(ctx, u.ID)
				if e != nil {
					return e
				}
				if !enabled {
					return problem(403, "mfa_required", "请先绑定 TOTP 再下载客户端证书")
				}
				// withPasswordProof already consumed the fresh TOTP atomically.
				data, e = a.certificates().ExportClient(ctx, id, in.ExportPassword)
				if e != nil {
					return e
				}
				action = "certificate.personal_exported"
			} else if e = a.certificates().Revoke(ctx, id, v.Version); e != nil {
				return e
			}
			return a.audit.Detail(ctx, u.ID, action, requestID(r), sourceIP(r), map[string]string{"id": id})
		})
		if e != nil {
			return e
		}
		w.Header().Set("Cache-Control", "no-store")
		if !export {
			w.WriteHeader(204)
			return nil
		}
		w.Header().Set("Content-Type", "application/x-pkcs12")
		w.Header().Set("Content-Disposition", `attachment; filename="client-`+id+`.p12"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, e = w.Write(data)
		return e
	}
}
