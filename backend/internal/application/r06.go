package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"os"
	"strings"
)

func (a *App) routesR06(handle func(string, func(http.ResponseWriter, *http.Request) error)) {
	a.routesAgentInstall(handle)
	a.routesJump(handle)
	a.routesGroups(handle)
	a.routesProxies(handle)
	a.routesAgentOperations(handle)
	handle("GET /api/admin/agent-binary/linux-amd64", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		file, e := os.Open(a.agentBinaryPath())
		if e != nil {
			return problem(503, "binary_unavailable", "当前部署未包含 Agent 程序")
		}
		defer file.Close()
		info, e := file.Stat()
		if e != nil {
			return e
		}
		w.Header().Set("Content-Disposition", `attachment; filename="nt-agent-linux-amd64"`)
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, info.Name(), info.ModTime(), file)
		return nil
	})

	handle("GET /api/agent/v1/connect", func(w http.ResponseWriter, r *http.Request) error {
		if r.Header.Get("Origin") != "" || token(r) != "" {
			return fault.Denied
		}
		if !a.allowAttempt("agent-connect:" + sourceIP(r)) {
			return problem(429, "rate_limited", "连接请求过多")
		}
		auth := nativeDeviceKey(r)
		if auth == "" {
			return fault.Denied
		}
		return a.connectivity.ServeAgent(w, r, auth)
	})

	handle("GET /api/admin/agent-registration-tokens", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		v, e := a.connectivity.Registrations(r.Context())
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	handle("POST /api/admin/agent-registration-tokens", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Name  string                    `json:"name"`
			Scope connectivity.NetworkScope `json:"networkScope"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		var v connectivity.Registration
		e := a.mutate(r, "agent.registration.created", func(ctx context.Context, u *identity.User) (string, error) {
			var e error
			v, e = a.connectivity.CreateRegistration(ctx, in.Name, u.ID, in.Scope)
			return v.ID, e
		})
		if e == nil {
			writeJSON(w, 201, v)
		}
		return e
	})
	handle("GET /api/admin/agent-gateways", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		v, e := a.connectivity.Devices(r.Context())
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	for _, kind := range []string{"agent-registration-tokens", "agent-gateways"} {
		handle("POST /api/admin/"+kind+"/{id}/revoke", func(w http.ResponseWriter, r *http.Request) error {
			id := r.PathValue("id")
			if !resource.ValidID(id) {
				return fault.Invalid
			}
			e := a.mutate(r, "agent."+kind+".revoked", func(ctx context.Context, u *identity.User) (string, error) {
				if kind == "agent-gateways" {
					return id, a.connectivity.RevokeDevice(ctx, id)
				}
				return id, a.connectivity.RevokeRegistration(ctx, id)
			})
			if e == nil {
				if kind == "agent-gateways" {
					a.connectivity.DisconnectAgent(id)
				}
				writeJSON(w, 204, nil)
			}
			return e
		})
	}
	handle("POST /api/agent/v1/enroll", func(w http.ResponseWriter, r *http.Request) error {
		if !a.allowAttempt("agent-enroll:" + sourceIP(r)) {
			return problem(429, "rate_limited", "注册请求过多")
		}
		var in connectivity.Enrollment
		if e := decode(w, r, &in); e != nil {
			return e
		}
		var v connectivity.Enrolled
		e := a.db.Within(r.Context(), func(ctx context.Context) error {
			var e error
			v, e = a.connectivity.Enroll(ctx, in)
			if e != nil {
				return e
			}
			return a.audit.Detail(ctx, "", "agent.enrolled", requestID(r), sourceIP(r), map[string]string{"target": v.AgentID})
		})
		if e == nil {
			writeJSON(w, 201, v)
		} else {
			_ = a.audit.Operation(r.Context(), "", "agent.enrolled", "failed", requestID(r), sourceIP(r))
		}
		return e
	})
}

func nativeDeviceKey(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if strings.HasPrefix(auth, "Basic ") {
		return r.Header.Get("X-NT-Agent-Key")
	}
	return ""
}
