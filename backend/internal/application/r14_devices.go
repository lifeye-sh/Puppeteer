package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
)

func (a *App) nativeDevices(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	if r.Method == "GET" {
		page := 1
		if raw := r.URL.Query().Get("page"); raw != "" {
			page, e = strconv.Atoi(raw)
			if e != nil || page < 1 || page > 100000 {
				return fault.Invalid
			}
		}
		var items []identity.NativeDevice
		var total int
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			var err error
			items, total, err = a.identity.NativeDevices(ctx, u.ID, (page-1)*20, 20)
			return err
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"items": items, "total": total, "page": page, "pageSize": 20})
		return nil
	}
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return fault.Invalid
	}
	var proof backupProof
	if e = decode(w, r, &proof); e != nil {
		return e
	}
	e = a.withPasswordProof(r, proof, "", func(ctx context.Context, current *identity.User) error {
		changed, e := a.identity.RevokeNativeDevice(ctx, current.ID, id)
		if e != nil {
			return e
		}
		// Pending reservations cannot reconnect. Existing streams observe the family
		// revocation on their bounded lease check, plus the explicit termination flag.
		if _, e = a.db.Exec(ctx, `UPDATE sessions SET terminate_requested=true WHERE closed_at IS NULL AND actor_id=$1 AND id IN (
   SELECT session_id FROM native_bridge_reservations WHERE family_id=$2
   UNION SELECT session_id FROM native_rdp_tickets WHERE family_id=$2)`, current.ID, id); e != nil {
			return e
		}
		if !changed {
			return nil
		}
		return a.audit.Detail(ctx, current.ID, "identity.native_device_revoked", requestID(r), sourceIP(r), map[string]string{"deviceId": id})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
