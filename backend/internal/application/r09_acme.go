package application

import (
	"context"
	"log/slog"
	"net/http"
	"next-terminal/backend/internal/modules/pki"
	"time"
)

func (a *App) routesR09ACME(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("GET /api/admin/acme", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "certificate.read"); e != nil {
			return e
		}
		accounts, e := a.certificates().Accounts(r.Context())
		if e != nil {
			return e
		}
		providers, e := a.certificates().Providers(r.Context())
		if e != nil {
			return e
		}
		orders, e := a.certificates().Orders(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"accounts": accounts, "providers": providers, "orders": orders})
		return nil
	})
	for _, kind := range []string{"accounts", "providers", "orders"} {
		save := func(w http.ResponseWriter, r *http.Request) error {
			u, e := a.require(r, "identity.manage")
			if e != nil {
				return e
			}
			var account pki.Account
			var provider pki.Provider
			var order pki.Order
			switch kind {
			case "accounts":
				e = decode(w, r, &account)
				account.ID = r.PathValue("id")
			case "providers":
				e = decode(w, r, &provider)
				provider.ID = r.PathValue("id")
			case "orders":
				e = decode(w, r, &order)
				order.ID = r.PathValue("id")
			}
			if e != nil {
				return e
			}
			var id string
			e = a.db.Within(r.Context(), func(ctx context.Context) error {
				var e error
				switch kind {
				case "accounts":
					id, e = a.certificates().SaveAccount(ctx, account)
				case "providers":
					id, e = a.certificates().SaveProvider(ctx, provider)
				case "orders":
					id, e = a.certificates().SaveOrder(ctx, order)
				}
				if e != nil {
					return e
				}
				return a.audit.Detail(ctx, u.ID, "acme."+kind+".saved", requestID(r), sourceIP(r), map[string]string{"id": id})
			})
			if e != nil {
				return e
			}
			writeJSON(w, 200, map[string]string{"id": id})
			return nil
		}
		h("POST /api/admin/acme/"+kind, save)
		h("PUT /api/admin/acme/"+kind+"/{id}", save)
	}
	h("POST /api/admin/acme/orders/{id}/retry", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in struct {
			Version int64 `json:"version"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			if e := a.certificates().RetryOrder(ctx, r.PathValue("id"), in.Version); e != nil {
				return e
			}
			return a.audit.Detail(ctx, u.ID, "acme.order.retry", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("id")})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(202)
		return nil
	})
	h("GET /api/admin/acme/orders/{id}/events", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "certificate.read"); e != nil {
			return e
		}
		rows, e := a.db.Rows(r.Context(), "SELECT id,status,message,created_at::text FROM certificate_order_events WHERE order_id=$1 ORDER BY id DESC LIMIT 200", r.PathValue("id"))
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id int64
			var status, message, created string
			if e = rows.Scan(&id, &status, &message, &created); e != nil {
				return e
			}
			out = append(out, map[string]any{"id": id, "status": status, "message": message, "createdAt": created})
		}
		if e = rows.Err(); e != nil {
			return e
		}
		writeJSON(w, 200, out)
		return nil
	})
}
func (a *App) RunCertificateMaintenance(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup, cancel := context.WithTimeout(ctx, 10*time.Second)
			if e := a.certificates().RecoverDNS(cleanup); e != nil && ctx.Err() == nil {
				slog.Warn("certificate DNS cleanup pending")
			}
			cancel()
			if e := a.certificates().ProcessOrder(ctx); e != nil && ctx.Err() == nil {
				slog.Error("certificate order state update failed")
			}
		}
	}
}
