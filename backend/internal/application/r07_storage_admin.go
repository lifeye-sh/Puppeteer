package application

import (
	"context"
	"fmt"
	"net/http"
	"next-terminal/backend/internal/modules/storage"
	"strconv"
	"syscall"
)

func (a *App) storageScope(r *http.Request, user string) (context.Context, string, error) {
	s := storage.New(a.db)
	if _, e := s.Ensure(r.Context(), user); e != nil {
		return nil, "", e
	}
	space := r.URL.Query().Get("space")
	session := r.URL.Query().Get("session")
	if session != "" {
		if e := a.db.Row(r.Context(), `SELECT b.space_id::text FROM session_storage b JOIN sessions s ON s.id=b.session_id WHERE s.id=$1 AND s.actor_id=$2 AND s.state IN ('connecting','connected') AND NOT s.terminate_requested AND s.lease_expires_at>now()`, session, user).Scan(&space); e != nil {
			return nil, "", storageError(syscall.EACCES)
		}
	}
	if space == "" {
		var e error
		space, e = s.Default(r.Context(), user)
		if e != nil {
			return nil, "", storageError(e)
		}
	}
	if e := s.Access(r.Context(), user, space, false); e != nil {
		return nil, "", storageError(e)
	}
	return storage.WithActor(r.Context(), user, "web", session, sourceIP(r)), space, nil
}
func (a *App) routesStorageAdmin(handle func(string, func(http.ResponseWriter, *http.Request) error)) {
	s := storage.New(a.db)
	handle("GET /api/storage/spaces", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		v, e := s.Spaces(r.Context(), u.ID, false)
		if e != nil {
			return e
		}
		p, e := s.Policy(r.Context(), u.ID)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"items": v, "policy": p})
		return nil
	})
	handle("PUT /api/storage/default", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		var in struct {
			Space string `json:"spaceId"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if e = s.SetDefault(r.Context(), u.ID, in.Space); e != nil {
			return storageError(e)
		}
		w.WriteHeader(204)
		return nil
	})
	handle("GET /api/admin/storages", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		v, e := s.Spaces(r.Context(), u.ID, true)
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("POST /api/admin/storages", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in struct {
			Name  string `json:"name"`
			Quota int64  `json:"quotaBytes"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		id, e := s.CreateSpace(r.Context(), u.ID, in.Name, in.Quota)
		if e != nil {
			return storageError(e)
		}
		if e = a.audit.Detail(r.Context(), u.ID, "storage.create", requestID(r), sourceIP(r), map[string]string{"spaceId": id}); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return nil
	})
	handle("PUT /api/admin/storages/{id}", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
			Quota   int64  `json:"quotaBytes"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if len(in.Name) == 0 || len([]rune(in.Name)) > 100 || in.Quota < 0 || in.Quota > 1<<40 {
			return storageError(syscall.EINVAL)
		}
		e = a.db.Within(r.Context(), func(c context.Context) error {
			var id string
			if e := a.db.Row(c, "SELECT user_id::text FROM user_storage WHERE user_id=$1 FOR UPDATE", r.PathValue("id")).Scan(&id); e != nil {
				return e
			}
			if _, e := a.db.Exec(c, "UPDATE storage_spaces SET name=$2,enabled=$3 WHERE id=$1", id, in.Name, in.Enabled); e != nil {
				return e
			}
			if _, e := a.db.Exec(c, "UPDATE user_storage SET quota_bytes=$2 WHERE user_id=$1", id, in.Quota); e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "storage.update", requestID(r), sourceIP(r), map[string]string{"spaceId": id})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	handle("GET /api/admin/storages/{id}/acl", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		rows, e := a.db.Rows(r.Context(), "SELECT a.user_id::text,u.username,a.writable FROM storage_acl a JOIN users u ON u.id=a.user_id WHERE a.space_id=$1 ORDER BY u.username", r.PathValue("id"))
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, name string
			var write bool
			if e = rows.Scan(&id, &name, &write); e != nil {
				return e
			}
			out = append(out, map[string]any{"userId": id, "username": name, "writable": write})
		}
		if e = rows.Err(); e != nil {
			return e
		}
		writeJSON(w, 200, out)
		return nil
	})
	handle("PUT /api/admin/storages/{id}/acl", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in struct {
			User     string `json:"userId"`
			Writable bool   `json:"writable"`
			Revoke   bool   `json:"revoke"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		e = a.db.Within(r.Context(), func(c context.Context) error {
			var shared bool
			if e := a.db.Row(c, "SELECT shared FROM storage_spaces WHERE id=$1 FOR UPDATE", r.PathValue("id")).Scan(&shared); e != nil {
				return e
			}
			if !shared {
				return storageError(syscall.EPERM)
			}
			if in.Revoke {
				_, e = a.db.Exec(c, "DELETE FROM storage_acl WHERE space_id=$1 AND user_id=$2", r.PathValue("id"), in.User)
			} else {
				_, e = a.db.Exec(c, "INSERT INTO storage_acl VALUES($1,$2,$3) ON CONFLICT(space_id,user_id) DO UPDATE SET writable=$3", r.PathValue("id"), in.User, in.Writable)
			}
			if e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "storage.acl", requestID(r), sourceIP(r), map[string]string{"spaceId": r.PathValue("id"), "userId": in.User, "revoke": fmt.Sprint(in.Revoke)})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	handle("GET /api/admin/users/{id}/channels", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		p, e := s.Policy(r.Context(), r.PathValue("id"))
		if e != nil {
			return e
		}
		writeJSON(w, 200, p)
		return nil
	})
	handle("PUT /api/admin/users/{id}/channels", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var p storage.Policy
		if e = decode(w, r, &p); e != nil {
			return e
		}
		e = a.db.Within(r.Context(), func(c context.Context) error {
			if e := s.SetPolicy(c, r.PathValue("id"), p); e != nil {
				return e
			}
			if _, e := a.db.Exec(c, "UPDATE sessions SET terminate_requested=true WHERE actor_id=$1 AND state IN ('reserved','connecting','connected') AND protocol='rdp'", r.PathValue("id")); e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "storage.channels", requestID(r), sourceIP(r), map[string]string{"userId": r.PathValue("id")})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	handle("GET /api/storage/events", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		admin := a.allowed(r.Context(), u, "audit.read") == nil
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		if page > 1000000 {
			page = 1000000
		}
		search := r.URL.Query().Get("search")
		if len(search) > 200 {
			return storageError(syscall.EINVAL)
		}
		const from = ` FROM storage_file_events ev JOIN users u ON u.id=ev.actor_id JOIN storage_spaces sp ON sp.id=ev.space_id WHERE (ev.actor_id=$1 OR $2) AND ($3='' OR ev.node_name ILIKE '%'||$3||'%' OR ev.operation ILIKE '%'||$3||'%' OR sp.name ILIKE '%'||$3||'%' OR u.username ILIKE '%'||$3||'%' OR ev.session_id=$3)`
		var total int64
		if e = a.db.Row(r.Context(), "SELECT count(*)"+from, u.ID, admin, search).Scan(&total); e != nil {
			return e
		}
		rows, e := a.db.Rows(r.Context(), `SELECT ev.id,ev.actor_id::text,u.username,ev.space_id::text,sp.name,ev.node_id,ev.operation,ev.channel,ev.session_id,ev.bytes,ev.created_at,ev.node_name`+from+` ORDER BY ev.id DESC LIMIT 50 OFFSET $4`, u.ID, admin, search, (page-1)*50)
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			v, e := rows.Values()
			if e != nil {
				return e
			}
			m := map[string]any{}
			for i, k := range []string{"id", "actorId", "username", "spaceId", "spaceName", "nodeId", "operation", "channel", "sessionId", "bytes", "createdAt", "name"} {
				m[k] = v[i]
			}
			out = append(out, m)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"items": out, "total": total, "page": page, "pageSize": 50})
		return nil
	})
}
