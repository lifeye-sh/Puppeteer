package application

import (
	"context"
	"log/slog"
	"net/http"
	"next-terminal/backend/internal/modules/system"
	"strconv"
	"time"
)

func (a *App) routesAccessRetention(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	a.routesAuditRetention(h)
	h("GET /api/admin/access-retention", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "settings.read"); e != nil {
			return e
		}
		v, e := a.system.AccessRetention(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	h("PUT /api/admin/access-retention", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in system.AccessRetention
		if e = decode(w, r, &in); e != nil {
			return e
		}
		e = a.db.Within(r.Context(), func(c context.Context) error {
			if e := a.system.SetAccessRetention(c, in, u.ID); e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "access.retention.updated", requestID(r), sourceIP(r), map[string]string{"days": strconv.Itoa(in.Days)})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
}
func (a *App) CleanupAccessRecords(ctx context.Context) error {
	return a.db.Within(ctx, func(c context.Context) error {
		if e := a.system.LockAuditRetention(c); e != nil {
			return e
		}
		config, e := a.system.LockedRetentionConfiguration(c)
		if e != nil {
			return e
		}
		ids, e := a.system.RetentionIDs(c, "access", config.Values.Int("access-log-saved-limit-days"))
		if e != nil {
			return e
		}
		for _, id := range ids {
			if e = a.audit.PurgeAccess(c, id); e != nil {
				return e
			}
			if e = a.sessions.MarkAuditPurged(c, id); e != nil {
				return e
			}
		}
		for _, category := range system.RetentionCategories() {
			if category.ID == "access" {
				continue
			}
			ids, e := a.system.RetentionIDs(c, category.ID, config.Values.Int(category.Key))
			if e != nil {
				return e
			}
			for _, id := range ids {
				if category.ID == "session" {
					if e = a.audit.PurgeSessionContent(c, id); e != nil {
						return e
					}
					continue
				}
				if e = a.system.PurgeCategoryRecord(c, category.ID, id); e != nil {
					return e
				}
			}
		}
		return nil
	})
}
func (a *App) RunAccessRetention(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			c, cancel := context.WithTimeout(ctx, 20*time.Second)
			e := a.CleanupAccessRecords(c)
			cancel()
			if e != nil && ctx.Err() == nil {
				slog.Error("access retention cleanup failed; transaction rolled back")
			}
		}
	}
}
