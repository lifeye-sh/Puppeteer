package application

import (
	"encoding/json"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/database"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
)

func databasePage(r *http.Request) (int, int, error) {
	page, _ := strconv.Atoi(r.URL.Query().Get("current"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if page > 100000 {
		return 0, 0, fault.Invalid
	}
	return (page - 1) * limit, limit, nil
}
func (a *App) registerDatabaseViews(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("GET /api/database-proxy/status", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.authenticated(r); e != nil {
			return e
		}
		_, port, _ := net.SplitHostPort(a.effectiveListenerAddress("mysql"))
		_, pgPort, _ := net.SplitHostPort(a.effectiveListenerAddress("postgres"))
		writeJSON(w, 200, map[string]any{"enabled": a.effectiveListenerAddress("mysql") != "", "ready": a.databaseProxyReady.Load(), "host": a.config.DatabaseProxyName, "port": port, "leaseSeconds": 5, "postgres": map[string]any{"enabled": a.effectiveListenerAddress("postgres") != "", "ready": a.postgresProxyReady.Load(), "host": a.config.DatabaseProxyName, "port": pgPort, "profile": "readonly-preview"}})
		return nil
	})
	h("GET /api/database-proxy/certificate", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.authenticated(r); e != nil {
			return e
		}
		id, _ := a.databaseProxyCertificate.Load().(string)
		if id == "" {
			return problem(404, "proxy_certificate_unavailable", "数据库代理证书尚未就绪")
		}
		v, e := a.certificates().Get(r.Context(), id)
		if e != nil {
			return e
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Disposition", `attachment; filename="next-terminal-mysql.pem"`)
		w.Header().Set("Cache-Control", "no-store")
		_, e = w.Write([]byte(v.PEM))
		return e
	})
	h("GET /api/admin/database-sql-logs", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		if e = a.allowed(r.Context(), u, "database.audit"); e != nil {
			return e
		}
		offset, limit, e := databasePage(r)
		if e != nil {
			return e
		}
		search := r.URL.Query().Get("search")
		if len(search) > 200 {
			return fault.Invalid
		}
		items, total, e := database.New(a.db).Logs(r.Context(), search, r.URL.Query().Get("state"), offset, limit)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"items": items, "total": total})
		return nil
	})
	h("GET /api/database-work-orders/{id}/results", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		if !resource.ValidID(r.PathValue("id")) {
			return fault.Invalid
		}
		service := database.New(a.db)
		v, e := service.Work(r.Context(), r.PathValue("id"), false)
		if e != nil {
			return e
		}
		if v.RequesterID != u.ID && a.allowed(r.Context(), u, "database.approve") != nil {
			return fault.Denied
		}
		index, _ := strconv.Atoi(r.URL.Query().Get("statement"))
		if index < 0 || index >= 100 {
			return fault.Invalid
		}
		offset, limit, e := databasePage(r)
		if e != nil {
			return e
		}
		results, e := service.WorkResults(r.Context(), v.ID, index, 1)
		if e != nil {
			return e
		}
		for i := range results {
			var data map[string]any
			if e = json.Unmarshal(results[i].Data, &data); e != nil {
				return e
			}
			if rows, ok := data["rows"].([]any); ok {
				data["retainedRows"] = len(rows)
				start := min(offset, len(rows))
				data["rows"] = rows[start:min(start+limit, len(rows))]
			}
			results[i].Data, e = json.Marshal(data)
			if e != nil {
				return e
			}
		}
		writeJSON(w, 200, map[string]any{"items": results})
		return nil
	})
}
