package application

import (
	"context"
	"github.com/google/uuid"
	"net/http"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
)

func (a *App) routesAuditRetention(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("GET /api/admin/audit-retention", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "settings.read"); e != nil {
			return e
		}
		v, e := a.system.Configuration(r.Context(), 0)
		if e != nil {
			return e
		}
		days := map[string]int{}
		for _, c := range system.RetentionCategories() {
			days[c.Key] = v.Values.Int(c.Key)
		}
		writeJSON(w, 200, map[string]any{"revision": v.ID, "days": days, "categories": system.RetentionCategories()})
		return nil
	})
	h("POST /api/admin/audit-retention/preview", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in struct {
			Revision int64          `json:"revision"`
			Days     map[string]int `json:"days"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		var result []system.RetentionPreview
		e = a.db.Within(r.Context(), func(c context.Context) error {
			var err error
			result, err = a.system.PreviewRetention(c, u.ID, in.Revision, in.Days)
			if err != nil {
				return err
			}
			return a.audit.Detail(c, u.ID, "audit.retention.preview", requestID(r), sourceIP(r), map[string]string{"revision": strconv.FormatInt(in.Revision, 10)})
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"revision": in.Revision, "items": result, "validForSeconds": 600})
		return nil
	})
	h("GET /api/admin/audit-holds", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		page := 1
		if raw := r.URL.Query().Get("page"); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 || n > 10000 {
				return fault.Invalid
			}
			page = n
		}
		items, total, e := a.system.AuditHolds(r.Context(), (page-1)*50)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"items": items, "total": total})
		return nil
	})
	h("POST /api/admin/audit-holds", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in struct {
			Category string `json:"category"`
			RecordID string `json:"recordId"`
			Reason   string `json:"reason"`
			Held     *bool  `json:"held"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if in.Held == nil {
			return fault.Invalid
		}
		if _, e = uuid.Parse(in.RecordID); e != nil {
			return fault.Invalid
		}
		e = a.db.Within(r.Context(), func(c context.Context) error {
			if e := a.system.SetAuditHold(c, in.Category, in.RecordID, in.Reason, u.ID, *in.Held); e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "audit.hold.updated", requestID(r), sourceIP(r), map[string]string{"category": in.Category, "recordId": in.RecordID, "held": strconv.FormatBool(*in.Held), "reason": in.Reason})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
}
