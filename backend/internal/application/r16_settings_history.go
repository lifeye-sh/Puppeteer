package application

import (
	"net/http"
	"net/url"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
)

func revisionQuery(q url.Values, key string, fallback int64) (int64, error) {
	values, exists := q[key]
	if !exists {
		return fallback, nil
	}
	if len(values) != 1 {
		return 0, fault.Invalid
	}
	n, e := strconv.ParseInt(values[0], 10, 64)
	if e != nil || n < 0 || strconv.FormatInt(n, 10) != values[0] {
		return 0, fault.Invalid
	}
	return n, nil
}
func (a *App) configurationHistory(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "settings.read"); e != nil {
		return e
	}
	before, e := revisionQuery(r.URL.Query(), "before", 0)
	if e != nil {
		return e
	}
	limit, e := revisionQuery(r.URL.Query(), "limit", 10)
	if e != nil || limit < 1 || limit > 50 {
		return fault.Invalid
	}
	v, e := a.system.ConfigurationHistory(r.Context(), before, int(limit))
	if e != nil {
		return e
	}
	writeJSON(w, 200, v)
	return nil
}
func (a *App) configurationDiff(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "settings.read"); e != nil {
		return e
	}
	id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil || id < 1 || strconv.FormatInt(id, 10) != r.PathValue("id") {
		return fault.Invalid
	}
	var from *int64
	if r.URL.Query().Has("from") {
		n, e := revisionQuery(r.URL.Query(), "from", 0)
		if e != nil {
			return e
		}
		from = &n
	}
	v, e := a.system.CompareConfiguration(r.Context(), id, from)
	if e != nil {
		return e
	}
	writeJSON(w, 200, v)
	return nil
}
