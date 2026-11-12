package application

import (
	"context"
	"net/http"
	"net/url"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/website"
	"next-terminal/backend/internal/platform/fault"
	"slices"
	"strconv"
	"strings"
)

func (a *App) websiteGrant(ctx context.Context, user, site string) error {
	subject, e := a.identity.Subject(ctx, user)
	if e != nil {
		return e
	}
	if !subject.Enabled {
		return fault.Denied
	}
	if subject.User.Type == "admin" {
		return nil
	}
	targets, e := a.authz.Targets(ctx, user, subject.DepartmentIDs)
	if e != nil {
		return e
	}
	scope, e := a.resources.Scope(ctx, site)
	if e != nil {
		return e
	}
	for _, id := range scope {
		if slices.Contains(targets, id) {
			return nil
		}
	}
	return fault.Denied
}
func (a *App) websiteURL(v website.Website) string {
	port := a.config.WebsitePublicPort
	if port == 0 || port == 443 {
		return "https://" + v.Domain
	}
	return "https://" + v.Domain + ":" + strconv.Itoa(port)
}
func (a *App) routesR09Websites(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("GET /api/admin/websites", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "website.read"); e != nil {
			return e
		}
		v, e := a.websites().List(r.Context())
		if e != nil {
			return e
		}
		for i := range v {
			v[i].URL = a.websiteURL(v[i]) + v[i].EntryPath
		}
		writeJSON(w, 200, v)
		return nil
	})
	h("GET /api/portal/websites", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		sites, e := a.websites().List(r.Context())
		if e != nil {
			return e
		}
		out := []map[string]any{}
		for _, v := range sites {
			if !v.Enabled || a.websiteGrant(r.Context(), u.ID, v.ID) != nil {
				continue
			}
			out = append(out, map[string]any{"id": v.ID, "name": v.Name, "domain": v.Domain, "mode": v.Mode, "url": a.websiteURL(v) + v.EntryPath, "groupId": v.GroupID})
		}
		writeJSON(w, 200, out)
		return nil
	})
	h("POST /api/admin/websites", a.saveWebsite)
	h("PUT /api/admin/websites/{id}", a.saveWebsite)
	h("DELETE /api/admin/websites/{id}", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		version, e := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
		if e != nil {
			return fault.Invalid
		}
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			tag, e := a.db.Exec(ctx, "UPDATE resource_registry SET deleted_at=now(),enabled=false,version=version+1 WHERE id=$1 AND version=$2 AND kind='website' AND deleted_at IS NULL", r.PathValue("id"), version)
			if e != nil {
				return e
			}
			if tag.RowsAffected() != 1 {
				return fault.Conflict
			}
			return a.audit.Detail(ctx, u.ID, "website.deleted", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("id")})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	h("POST /api/portal/websites/{id}/login-code", a.websiteLoginCode)
}
func (a *App) saveWebsite(w http.ResponseWriter, r *http.Request) error {
	u, e := a.require(r, "identity.manage")
	if e != nil {
		return e
	}
	var in website.Website
	if e = decode(w, r, &in); e != nil {
		return e
	}
	in.ID = r.PathValue("id")
	settings, e := a.websites().Settings(r.Context())
	if e != nil {
		return e
	}
	if e = website.Validate(&in, settings.ConsoleURL); e != nil {
		return e
	}
	hash := ""
	if in.Password != "" {
		release, e := a.hashPermit(r)
		if e != nil {
			return e
		}
		hash = identity.HashPassword(in.Password)
		release()
	}
	var id string
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		certID := in.CertificateID
		if certID == "" {
			certID = settings.DefaultCertificateID
		}
		if in.Enabled || certID != "" {
			cert, e := a.certificates().Get(ctx, certID)
			if e != nil {
				return e
			}
			if cert.Kind != "server" {
				return fault.Invalid
			}
			pair, e := a.certificates().TLS(ctx, certID)
			if e != nil {
				return e
			}
			if pair.Leaf.VerifyHostname(in.Domain) != nil {
				return problem(422, "certificate_domain", "服务器证书未覆盖网站域名")
			}
		}
		if in.ClientCAID != "" {
			ca, e := a.certificates().Get(ctx, in.ClientCAID)
			if e != nil {
				return e
			}
			if ca.Kind != "client_ca" || ca.RevokedAt != nil {
				return fault.Invalid
			}
		}
		var e error
		id, e = a.websites().Save(ctx, in, hash)
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "website.saved", requestID(r), sourceIP(r), map[string]string{"id": id, "domain": in.Domain})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]string{"id": id})
	return nil
}
func (a *App) websiteLoginCode(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return fault.Invalid
	}
	v, e := a.websites().Get(r.Context(), id)
	if e != nil {
		return e
	}
	if !v.Enabled {
		return fault.Denied
	}
	if e = a.websiteGrant(r.Context(), u.ID, id); e != nil {
		return e
	}
	var in struct {
		State  string `json:"state"`
		Return string `json:"return"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	if len(in.State) != 64 || strings.Trim(in.State, "0123456789abcdef") != "" || !website.SafePath(in.Return) {
		return fault.Invalid
	}
	code := identity.NewToken()
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		if _, e := a.db.Exec(ctx, `INSERT INTO website_login_codes(digest,website_id,user_id,state_digest,return_path,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '60 seconds')`, identity.Digest(code), id, u.ID, identity.Digest(in.State), in.Return); e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "website.login_code", requestID(r), sourceIP(r), map[string]string{"id": id})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]string{"url": a.websiteURL(v) + "/_nt/callback?code=" + url.QueryEscape(code)})
	return nil
}
