package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
)

func (a *App) routesAgentOperations(handle func(string, func(http.ResponseWriter, *http.Request) error)) {
	handle("POST /api/admin/agent-gateways/{id}/update", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Action string `json:"action"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		id := r.PathValue("id")
		if !resource.ValidID(id) {
			return fault.Invalid
		}
		e := a.mutate(r, "agent.update_requested", func(ctx context.Context, u *identity.User) (string, error) {
			return id, a.connectivity.RequestUpdate(ctx, id, in.Action)
		})
		if e == nil {
			w.WriteHeader(204)
		}
		return e
	})
	handle("GET /api/admin/agent-observations", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		v, e := a.connectivity.Observations(r.Context())
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	handle("POST /api/admin/agent-gateways/{id}/rotate", func(w http.ResponseWriter, r *http.Request) error {
		id := r.PathValue("id")
		if !resource.ValidID(id) {
			return fault.Invalid
		}
		e := a.mutate(r, "agent.rotation_requested", func(ctx context.Context, u *identity.User) (string, error) {
			return id, a.connectivity.RequestRotation(ctx, id)
		})
		if e == nil {
			w.WriteHeader(204)
		}
		return e
	})
	handle("POST /api/agent/v1/rotate", func(w http.ResponseWriter, r *http.Request) error {
		if r.Header.Get("Origin") != "" || token(r) != "" || nativeDeviceKey(r) == "" {
			return fault.Denied
		}
		if !a.allowAttempt("agent-rotate:" + sourceIP(r)) {
			return problem(429, "rate_limited", "轮换请求过多")
		}
		var in struct {
			NextKey string `json:"nextKey"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		e := a.db.Within(r.Context(), func(ctx context.Context) error {
			id, e := a.connectivity.RotateDevice(ctx, nativeDeviceKey(r), in.NextKey)
			if e != nil {
				return e
			}
			return a.audit.Detail(ctx, "", "agent.key_rotated", requestID(r), sourceIP(r), map[string]string{"target": id})
		})
		if e == nil {
			w.WriteHeader(204)
		}
		return e
	})
}
