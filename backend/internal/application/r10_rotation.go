package application

import (
	"context"
	"fmt"
	"net/http"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/vault"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/jobs"
	"strings"
)

func (a *App) routesR10Rotation(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("POST /api/rotation-runs/{id}/targets/{target}/reconcile", a.reconcileRotation)
	h("GET /api/rotation-runs", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "credential.rotate")
		if e != nil {
			return e
		}
		page, e := taskPage(r)
		if e != nil {
			return e
		}
		items, total, e := a.vault.RotationRuns(r.Context(), u.ID, u.Type == "admin", page)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"items": items, "total": total})
		return nil
	})
	h("GET /api/rotation-runs/{id}", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "credential.rotate")
		if e != nil {
			return e
		}
		if !resource.ValidID(r.PathValue("id")) {
			return fault.Invalid
		}
		v, e := a.vault.RotationRun(r.Context(), r.PathValue("id"))
		if e != nil {
			return e
		}
		if v.ActorID != u.ID && u.Type != "admin" {
			return fault.Denied
		}
		writeJSON(w, 200, v)
		return nil
	})
	h("POST /api/rotation-runs", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "credential.rotate")
		if e != nil {
			return e
		}
		var in struct {
			Key      string   `json:"key"`
			AssetIDs []string `json:"assetIds"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if !resource.ValidID(in.Key) || len(in.AssetIDs) < 1 || len(in.AssetIDs) > 20 {
			return fault.Invalid
		}
		var id string
		e = a.db.Within(r.Context(), func(c context.Context) error {
			var fresh bool
			var e error
			id, fresh, e = a.vault.CreateRotation(c, u.ID, in.Key, sourceIP(r))
			if e != nil || !fresh {
				return e
			}
			seen := map[string]bool{}
			for _, assetID := range in.AssetIDs {
				if !resource.ValidID(assetID) || seen[assetID] {
					return fault.Invalid
				}
				seen[assetID] = true
				asset, e := a.sessionAsset(c, u, assetID)
				if e != nil {
					return e
				}
				if asset.Protocol != "ssh" {
					return fault.Invalid
				}
				if e = a.enforcePolicy(c, u, asset.ID, sourceIP(r), "connect", ""); e != nil {
					return e
				}
				if e = a.enforcePolicy(c, u, asset.ID, sourceIP(r), "execute", "credential.rotate"); e != nil {
					return e
				}
				cred, e := a.vault.Get(c, asset.CredentialID)
				if e != nil {
					return e
				}
				trust, e := a.connectivity.Get(c, asset.ID)
				if e != nil || trust.Host != asset.Host || trust.Port != asset.Port {
					return problem(409, "host_key_untrusted", "请先确认目标 SSH 主机指纹")
				}
				// A pinned host identity plus username detects DNS aliases and alternate ports.
				account := fmt.Sprintf("%s:%s", trust.Fingerprint, strings.ToLower(cred.Username))
				_, e = a.vault.AddRotationTarget(c, vault.RotationTarget{RunID: id, AssetID: asset.ID, AssetVersion: asset.Version, CredentialID: cred.ID, CredentialVersion: cred.Version, AccountKey: account, Kind: cred.Kind})
				if e != nil {
					return e
				}
			}
			return a.audit.Detail(c, u.ID, "rotation.requested", requestID(r), sourceIP(r), map[string]string{"id": id})
		})
		if e != nil {
			return e
		}
		writeJSON(w, 201, map[string]string{"id": id})
		return nil
	})
	h("POST /api/rotation-runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "credential.rotate")
		if e != nil {
			return e
		}
		if !resource.ValidID(r.PathValue("id")) {
			return fault.Invalid
		}
		run, e := a.vault.RotationRun(r.Context(), r.PathValue("id"))
		if e != nil {
			return e
		}
		if run.ActorID != u.ID && u.Type != "admin" {
			return fault.Denied
		}
		e = a.db.Within(r.Context(), func(c context.Context) error {
			if e := jobs.New(a.db).Cancel(c, run.ID); e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "rotation.cancel_requested", requestID(r), sourceIP(r), map[string]string{"id": run.ID})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
}
