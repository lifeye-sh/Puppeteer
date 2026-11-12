package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"next-terminal/backend/internal/modules/operations"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"time"
)

func (a *App) routesR16Monitoring(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("GET /api/admin/monitor-metrics", func(w http.ResponseWriter, r *http.Request) error {
		if _, err := a.require(r, "identity.manage"); err != nil {
			return err
		}
		writeJSON(w, 200, operations.MonitorMetrics())
		return nil
	})
	h("GET /api/admin/monitoring/overview", func(w http.ResponseWriter, r *http.Request) error {
		if _, err := a.require(r, "identity.manage"); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, a.operations().Overview(ctx, a.configurationNode))
		return nil
	})
	h("GET /api/admin/monitor-rules", func(w http.ResponseWriter, r *http.Request) error {
		if _, err := a.require(r, "identity.manage"); err != nil {
			return err
		}
		v, err := a.operations().MonitorRules(r.Context())
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	save := func(w http.ResponseWriter, r *http.Request) error {
		u, err := a.require(r, "identity.manage")
		if err != nil {
			return err
		}
		var v operations.MonitorRule
		if err = decode(w, r, &v); err != nil {
			return err
		}
		v.ID = r.PathValue("id")
		if (v.ID != "" && !resource.ValidID(v.ID)) || (v.AgentID != "" && !resource.ValidID(v.AgentID)) {
			return fault.Invalid
		}
		var id string
		err = a.db.Within(r.Context(), func(c context.Context) error {
			var e error
			id, e = a.operations().SaveMonitorRule(c, v, u.ID)
			if e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "monitor.rule.save", requestID(r), sourceIP(r), map[string]string{"ruleId": id})
		})
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return nil
	}
	h("POST /api/admin/monitor-rules", save)
	h("PUT /api/admin/monitor-rules/{id}", save)
	h("DELETE /api/admin/monitor-rules/{id}", func(w http.ResponseWriter, r *http.Request) error {
		u, err := a.require(r, "identity.manage")
		if err != nil {
			return err
		}
		id := r.PathValue("id")
		version, err := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
		if err != nil || version < 1 || !resource.ValidID(id) {
			return fault.Invalid
		}
		err = a.db.Within(r.Context(), func(c context.Context) error {
			if e := a.operations().DeleteMonitorRule(c, id, version); e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "monitor.rule.delete", requestID(r), sourceIP(r), map[string]string{"ruleId": id})
		})
		if err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	})
	h("GET /api/admin/system-events", func(w http.ResponseWriter, r *http.Request) error {
		if _, err := a.require(r, "identity.manage"); err != nil {
			return err
		}
		before := time.Now()
		id := "ffffffff-ffff-ffff-ffff-ffffffffffff"
		if text := r.URL.Query().Get("before"); text != "" {
			var err error
			before, err = time.Parse(time.RFC3339Nano, text)
			if err != nil {
				return fault.Invalid
			}
			id = r.URL.Query().Get("beforeId")
			if !resource.ValidID(id) {
				return fault.Invalid
			}
		}
		items, err := a.operations().SystemEvents(r.Context(), before, id, 101)
		if err != nil {
			return err
		}
		more := len(items) > 100
		if more {
			items = items[:100]
		}
		writeJSON(w, 200, map[string]any{"items": items, "hasMore": more})
		return nil
	})
	h("POST /api/admin/system-events/{id}/acknowledge", func(w http.ResponseWriter, r *http.Request) error {
		u, err := a.require(r, "identity.manage")
		if err != nil {
			return err
		}
		id := r.PathValue("id")
		if !resource.ValidID(id) {
			return fault.Invalid
		}
		err = a.db.Within(r.Context(), func(c context.Context) error {
			if e := a.operations().AcknowledgeEvent(c, id, u.ID); e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "monitor.event.acknowledge", requestID(r), sourceIP(r), map[string]string{"eventId": id})
		})
		if err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	})
}

func (a *App) evaluateMonitors(ctx context.Context) error {
	return a.operations().EvaluateMonitors(ctx, func(c context.Context, id, kind, actor, name string) error {
		label := "告警触发"
		if kind == "recovered" {
			label = "告警恢复"
		}
		payload, _ := json.Marshal(map[string]string{"title": "Puppeteer 运维告警", "text": fmt.Sprintf("%s：%s。事件 %s，请登录控制台查看详情。", label, name, id)})
		_, err := a.db.Exec(c, `INSERT INTO notification_events(event_key,event_type,system_event_id,actor_id,payload) VALUES($1,$2,$3,$4,$5) ON CONFLICT(event_key) DO NOTHING`, "monitor:"+id, "monitor."+kind, id, actor, payload)
		return err
	})
}
func (a *App) RunMonitoring(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c, cancel := context.WithTimeout(ctx, 8*time.Second)
			err := a.evaluateMonitors(c)
			cancel()
			if err != nil && ctx.Err() == nil {
				slog.Error("monitor evaluation failed")
			}
		}
	}
}

func (a *App) operations() *operations.Service { return operations.New(a.db) }
