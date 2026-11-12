package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/platform/fault"
)

func (a *App) routesR09WebsiteRecords(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("GET /api/admin/websites/{id}/logs", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "audit.read"); e != nil {
			return e
		}
		rows, e := a.db.Rows(r.Context(), `SELECT id,client_ip,method,path,status,reason,created_at::text FROM website_access_logs WHERE website_id=$1 ORDER BY id DESC LIMIT 200`, r.PathValue("id"))
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id int64
			var ip, method, path, reason, created string
			var status int
			if e = rows.Scan(&id, &ip, &method, &path, &status, &reason, &created); e != nil {
				return e
			}
			out = append(out, map[string]any{"id": id, "ip": ip, "method": method, "path": path, "status": status, "reason": reason, "createdAt": created})
		}
		if e = rows.Err(); e != nil {
			return e
		}
		writeJSON(w, 200, out)
		return nil
	})
	h("GET /api/portal/websites/{id}/temporary-ips", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		if e = a.websiteGrant(r.Context(), u.ID, r.PathValue("id")); e != nil {
			return e
		}
		rows, e := a.db.Rows(r.Context(), `SELECT id::text,host(ip),expires_at::text,absolute_expires_at::text FROM website_temporary_ips WHERE website_id=$1 AND (user_id=$2 OR $3) AND revoked_at IS NULL ORDER BY created_at DESC LIMIT 500`, r.PathValue("id"), u.ID, u.Type == "admin")
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []map[string]string{}
		for rows.Next() {
			var id, ip, end, max string
			if e = rows.Scan(&id, &ip, &end, &max); e != nil {
				return e
			}
			out = append(out, map[string]string{"id": id, "ip": ip, "expiresAt": end, "absoluteExpiresAt": max})
		}
		if e = rows.Err(); e != nil {
			return e
		}
		writeJSON(w, 200, out)
		return nil
	})
	h("DELETE /api/portal/websites/{id}/temporary-ips/{ip}", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			tag, e := a.db.Exec(ctx, `UPDATE website_temporary_ips SET revoked_at=now() WHERE id=$1 AND website_id=$2 AND (user_id=$3 OR $4) AND revoked_at IS NULL`, r.PathValue("ip"), r.PathValue("id"), u.ID, u.Type == "admin")
			if e != nil {
				return e
			}
			if tag.RowsAffected() != 1 {
				return fault.Denied
			}
			return a.audit.Detail(ctx, u.ID, "website.temporary_ip.revoked", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("ip")})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
}
