package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/automation"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/jobs"
	"strconv"
	"time"
)

func taskPage(r *http.Request) (int, error) {
	v := r.URL.Query().Get("page")
	if v == "" {
		return 1, nil
	}
	n, e := strconv.Atoi(v)
	if e != nil || n < 1 || n > 100000 {
		return 0, fault.Invalid
	}
	return n, nil
}
func (a *App) taskAsset(ctx context.Context, owner, assetID, ip, command string) (resource.Asset, *identity.User, error) {
	subject, e := a.identity.Subject(ctx, owner)
	if e != nil {
		return resource.Asset{}, nil, e
	}
	if !subject.Enabled {
		return resource.Asset{}, nil, fault.Denied
	}
	u := &subject.User
	if e = a.allowed(ctx, u, "task.manage"); e != nil {
		return resource.Asset{}, nil, e
	}
	v, e := a.sessionAsset(ctx, u, assetID)
	if e != nil {
		return v, u, e
	}
	if v.Protocol != "ssh" {
		return v, u, fault.Invalid
	}
	if e = a.enforcePolicy(ctx, u, v.ID, ip, "connect", ""); e != nil {
		return v, u, e
	}
	e = a.enforcePolicy(ctx, u, v.ID, ip, "execute", command)
	return v, u, e
}
func (a *App) routesR10Tasks(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	s := automation.New(a.db)
	q := jobs.New(a.db)
	h("GET /api/tasks", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "task.manage")
		if e != nil {
			return e
		}
		p, e := taskPage(r)
		if e != nil {
			return e
		}
		v, total, e := s.Schedules(r.Context(), u.ID, u.Type == "admin", p)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"items": v, "total": total})
		return nil
	})
	h("POST /api/tasks/preview", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "task.manage"); e != nil {
			return e
		}
		var in struct {
			Cron     string `json:"cron"`
			Timezone string `json:"timezone"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		v, e := automation.NextRuns(in.Cron, in.Timezone, time.Now())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	save := func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "task.manage")
		if e != nil {
			return e
		}
		var in automation.Schedule
		if e = decode(w, r, &in); e != nil {
			return e
		}
		in.ID = r.PathValue("id")
		in.OwnerID = u.ID
		in.SourceIP = sourceIP(r)
		if e = automation.ValidateSchedule(&in); e != nil {
			return e
		}
		if in.Kind == "credential.rotate" {
			if e = a.allowed(r.Context(), u, "credential.rotate"); e != nil {
				return e
			}
		}
		for _, id := range in.AssetIDs {
			if _, _, e = a.taskAsset(r.Context(), u.ID, id, in.SourceIP, in.Command); e != nil {
				return e
			}
		}
		var id string
		e = a.db.Within(r.Context(), func(c context.Context) error {
			var e error
			id, e = s.SaveSchedule(c, in)
			if e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "task.saved", requestID(r), sourceIP(r), map[string]string{"id": id})
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return nil
	}
	h("POST /api/tasks", save)
	h("PUT /api/tasks/{id}", save)
	h("DELETE /api/tasks/{id}", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "task.manage")
		if e != nil {
			return e
		}
		v, e := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
		if e != nil || v < 1 {
			return fault.Invalid
		}
		e = a.db.Within(r.Context(), func(c context.Context) error {
			if _, e := s.Schedule(c, r.PathValue("id"), true); e != nil {
				return e
			}
			if e := s.DeleteSchedule(c, r.PathValue("id"), u.ID, v); e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "task.deleted", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("id")})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	h("POST /api/tasks/{id}/trigger", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "task.manage")
		if e != nil {
			return e
		}
		var in struct {
			Key     string `json:"key"`
			Version int64  `json:"version"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if !resource.ValidID(in.Key) {
			return fault.Invalid
		}
		var id string
		e = a.db.Within(r.Context(), func(c context.Context) error {
			v, e := s.Schedule(c, r.PathValue("id"), true)
			if e != nil {
				return e
			}
			if v.OwnerID != u.ID {
				return fault.Denied
			}
			if v.Version != in.Version {
				return fault.Conflict
			}
			// Bind manual invocation to this request's source, not the original definition's IP.
			v.SourceIP = sourceIP(r)
			id, e = s.Trigger(c, q, v, "manual:"+in.Key, nil, "")
			if e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "task.triggered", requestID(r), sourceIP(r), map[string]string{"id": id, "scheduleId": v.ID})
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return nil
	})
	h("GET /api/task-runs", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "task.manage")
		if e != nil {
			return e
		}
		page, e := taskPage(r)
		if e != nil {
			return e
		}
		v, total, e := s.Runs(r.Context(), u.ID, u.Type == "admin", r.URL.Query().Get("scheduleId"), page)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"items": v, "total": total})
		return nil
	})
	h("GET /api/task-runs/{id}", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "task.manage")
		if e != nil {
			return e
		}
		v, e := s.Run(r.Context(), r.PathValue("id"))
		if e != nil {
			return e
		}
		if v.ActorID != u.ID && u.Type != "admin" {
			return fault.Denied
		}
		writeJSON(w, 200, v)
		return nil
	})
	h("POST /api/task-runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "task.manage")
		if e != nil {
			return e
		}
		v, e := s.Run(r.Context(), r.PathValue("id"))
		if e != nil {
			return e
		}
		if v.ActorID != u.ID && u.Type != "admin" {
			return fault.Denied
		}
		e = a.db.Within(r.Context(), func(c context.Context) error {
			if e := q.Cancel(c, v.ID); e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "task.cancel_requested", requestID(r), sourceIP(r), map[string]string{"id": v.ID})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
}
