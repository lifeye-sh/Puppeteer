package application

import (
	"encoding/csv"
	"net/http"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"time"
	_ "time/tzdata"
)

func (a *App) sessionStatistics(w http.ResponseWriter, r *http.Request) error {
	u, err := a.authenticated(r)
	if err != nil {
		return err
	}
	actor := u.ID
	q := r.URL.Query()
	if q.Get("all") == "true" {
		if err = a.allowed(r.Context(), u, "session.read"); err != nil {
			return err
		}
		actor = ""
	}
	zone := q.Get("timezone")
	if zone == "" {
		zone = "UTC"
	}
	if len(zone) > 64 || zone == "Local" {
		return fault.Invalid
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return fault.Invalid
	}
	to := q.Get("to")
	if to == "" {
		to = time.Now().In(loc).Format("2006-01-02")
	}
	end, err := time.ParseInLocation("2006-01-02", to, loc)
	if err != nil {
		return fault.Invalid
	}
	from := q.Get("from")
	if from == "" {
		from = end.AddDate(0, 0, -13).Format("2006-01-02")
	}
	start, err := time.ParseInLocation("2006-01-02", from, loc)
	if err != nil {
		return fault.Invalid
	}
	// Calendar-day bound, independent of DST transitions.
	if start.After(end) || end.After(start.AddDate(0, 0, 365)) {
		return fault.Invalid
	}
	format := q.Get("format")
	if format != "" && format != "csv" {
		return fault.Invalid
	}
	v, err := a.sessions.Statistics(r.Context(), actor, start, end.AddDate(0, 0, 1), zone)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="access-statistics.csv"`)
		writer := csv.NewWriter(w)
		if err = writer.Write([]string{"date", "sessions", "users", "assets", "failed", "operation_seconds"}); err != nil {
			return err
		}
		for _, d := range v.Days {
			if err = writer.Write([]string{d.Date, strconv.FormatInt(d.Sessions, 10), strconv.FormatInt(d.Users, 10), strconv.FormatInt(d.Assets, 10), strconv.FormatInt(d.Failed, 10), strconv.FormatInt(d.Seconds, 10)}); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()
	}
	writeJSON(w, 200, v)
	return nil
}

func (a *App) assetStatistics(w http.ResponseWriter, r *http.Request) error {
	f, err := a.resourceFilter(r, r.URL.Query().Get("all") != "true")
	if err != nil {
		return err
	}
	v, err := a.resources.ProtocolCounts(r.Context(), f)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"protocols": v})
	return nil
}
