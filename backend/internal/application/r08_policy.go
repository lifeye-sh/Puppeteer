package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/authorization"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
)

func (a *App) policyDecision(ctx context.Context, u *identity.User, resourceID, ip, action, command string) (authorization.Decision, error) {
	deps, e := a.identity.DepartmentIDs(ctx, u.ID)
	if e != nil {
		return authorization.Decision{}, e
	}
	scope, e := a.resources.Scope(ctx, resourceID)
	if e != nil {
		return authorization.Decision{}, e
	}
	forced, e := a.authz.ForcedPolicies(ctx, u.ID, deps, scope)
	if e != nil {
		return authorization.Decision{}, e
	}
	return a.authz.Evaluate(ctx, authorization.Facts{UserID: u.ID, DepartmentIDs: deps, ResourceIDs: scope, ForcedIDs: forced, IP: ip, Action: action, Command: command})
}
func (a *App) enforcePolicy(ctx context.Context, u *identity.User, resourceID, ip, action, command string) error {
	d, e := a.policyDecision(ctx, u, resourceID, ip, action, command)
	if e != nil {
		return e
	}
	if !d.Allowed {
		return problem(403, d.Reason, "访问策略禁止此操作（"+d.Reason+"）")
	}
	return nil
}
func (a *App) routesR08Policies(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	for _, k := range []string{"access-policy", "strategy", "command-filter", "ip-set"} {
		kind := k
		path := "/api/admin/policies/" + kind
		h("GET "+path, func(w http.ResponseWriter, r *http.Request) error {
			if _, e := a.require(r, "policy.read"); e != nil {
				return e
			}
			out, e := a.authz.Policies(r.Context(), kind)
			if e != nil {
				return e
			}
			writeJSON(w, 200, out)
			return nil
		})
		save := func(w http.ResponseWriter, r *http.Request) error {
			u, e := a.require(r, "identity.manage")
			if e != nil {
				return e
			}
			var p authorization.PolicyDocument
			if e = decode(w, r, &p); e != nil {
				return e
			}
			p.ID = r.PathValue("id")
			var id string
			e = a.db.Within(r.Context(), func(c context.Context) error {
				var e error
				id, e = a.authz.SavePolicy(c, kind, p)
				if e != nil {
					return e
				}
				return a.audit.Detail(c, u.ID, "policy.saved", requestID(r), sourceIP(r), map[string]string{"kind": kind, "id": id})
			})
			if e != nil {
				return e
			}
			writeJSON(w, 200, map[string]string{"id": id})
			return nil
		}
		h("POST "+path, save)
		h("PUT "+path+"/{id}", save)
		h("DELETE "+path+"/{id}", func(w http.ResponseWriter, r *http.Request) error {
			u, e := a.require(r, "identity.manage")
			if e != nil {
				return e
			}
			version, e := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
			if e != nil {
				return fault.Invalid
			}
			e = a.db.Within(r.Context(), func(c context.Context) error {
				if e := a.authz.DeletePolicy(c, kind, r.PathValue("id"), version); e != nil {
					return e
				}
				return a.audit.Detail(c, u.ID, "policy.deleted", requestID(r), sourceIP(r), map[string]string{"kind": kind, "id": r.PathValue("id")})
			})
			if e != nil {
				return e
			}
			w.WriteHeader(204)
			return nil
		})
	}
	h("POST /api/admin/policies/explain", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		var in struct {
			UserID     string `json:"userId"`
			ResourceID string `json:"resourceId"`
			IP         string `json:"ip"`
			Action     string `json:"action"`
			Command    string `json:"command"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		subject, e := a.identity.Subject(r.Context(), in.UserID)
		if e != nil {
			return e
		}
		if !subject.Enabled {
			return fault.Denied
		}
		d, e := a.policyDecision(r.Context(), &subject.User, in.ResourceID, in.IP, in.Action, in.Command)
		if e != nil {
			return e
		}
		writeJSON(w, 200, d)
		return nil
	})
	h("GET /api/admin/geodata", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "policy.read"); e != nil {
			return e
		}
		out, e := a.authz.GeoVersions(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, out)
		return nil
	})
	h("POST /api/admin/geodata", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in struct {
			Name    string                   `json:"name"`
			Entries []authorization.GeoEntry `json:"entries"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		var id string
		e = a.db.Within(r.Context(), func(c context.Context) error {
			var e error
			id, e = a.authz.ImportGeo(c, in.Name, in.Entries)
			if e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "geodata.imported", requestID(r), sourceIP(r), map[string]string{"id": id})
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return nil
	})
}
