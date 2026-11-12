package application

import (
	"net/http"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"time"
)

func (a *App) nativeForwardDetails(w http.ResponseWriter, r *http.Request) error {
	v, _, e := a.inspectSession(r, "session.read")
	if e != nil {
		return e
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("current"))
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		return fault.Invalid
	}
	var total int
	if e = a.db.Row(r.Context(), `SELECT count(*) FROM native_forward_streams WHERE session_id=$1`, v.ID).Scan(&total); e != nil {
		return e
	}
	rows, e := a.db.Rows(r.Context(), `SELECT id,host,port,address,state,bytes_to_target,bytes_from_target,started_at,ended_at FROM native_forward_streams WHERE session_id=$1 ORDER BY started_at DESC,id LIMIT 20 OFFSET $2`, v.ID, (page-1)*20)
	if e != nil {
		return e
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, host, address, state string
		var port int
		var sent, received int64
		var started time.Time
		var ended *time.Time
		if e = rows.Scan(&id, &host, &port, &address, &state, &sent, &received, &started, &ended); e != nil {
			return e
		}
		items = append(items, map[string]any{"id": id, "host": host, "port": port, "address": address, "state": state, "bytesToTarget": sent, "bytesFromTarget": received, "startedAt": started, "endedAt": ended})
	}
	if e = rows.Err(); e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total})
	return nil
}
