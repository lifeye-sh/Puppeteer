package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/automation"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
)

func (a *App) routesR10Snippets(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	service := automation.New(a.db)
	h("GET /api/snippets", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		scope := r.URL.Query().Get("scope")
		if scope == "" {
			scope = "all"
		}
		if scope != "all" && scope != "mine" && scope != "system" {
			return fault.Invalid
		}
		page := 1
		if v := r.URL.Query().Get("page"); v != "" {
			page, e = strconv.Atoi(v)
			if e != nil || page < 1 || page > 100000 {
				return fault.Invalid
			}
		}
		search := r.URL.Query().Get("search")
		if len(search) > 500 {
			return fault.Invalid
		}
		out, e := service.Snippets(r.Context(), u.ID, scope, search, page)
		if e != nil {
			return e
		}
		writeJSON(w, 200, out)
		return nil
	})
	for _, sys := range []bool{false, true} {
		system := sys
		path := "/api/snippets"
		if system {
			path = "/api/admin/snippets"
		}
		save := func(w http.ResponseWriter, r *http.Request) error {
			u, e := a.authenticated(r)
			if e != nil {
				return e
			}
			if system {
				u, e = a.require(r, "snippet.manage")
				if e != nil {
					return e
				}
			}
			var in automation.Snippet
			if e = decode(w, r, &in); e != nil {
				return e
			}
			in.ID = r.PathValue("id")
			var id string
			e = a.db.Within(r.Context(), func(ctx context.Context) error {
				id, e = service.SaveSnippet(ctx, u.ID, system, in)
				if e != nil {
					return e
				}
				// Do not duplicate script bodies in general operation logs.
				return a.audit.Detail(ctx, u.ID, "snippet.saved", requestID(r), sourceIP(r), map[string]string{"id": id, "system": strconv.FormatBool(system)})
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
			u, e := a.authenticated(r)
			if e != nil {
				return e
			}
			if system {
				u, e = a.require(r, "snippet.manage")
				if e != nil {
					return e
				}
			}
			v, e := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
			if e != nil || v < 1 {
				return fault.Invalid
			}
			e = a.db.Within(r.Context(), func(ctx context.Context) error {
				if e := service.DeleteSnippet(ctx, u.ID, system, r.PathValue("id"), v); e != nil {
					return e
				}
				return a.audit.Detail(ctx, u.ID, "snippet.deleted", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("id")})
			})
			if e != nil {
				return e
			}
			w.WriteHeader(204)
			return nil
		})
	}
}
