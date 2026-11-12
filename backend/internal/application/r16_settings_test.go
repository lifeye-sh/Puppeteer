package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"next-terminal/backend/internal/modules/system"
	"strconv"
	"sync"
	"testing"
)

func settingsRequest(t *testing.T, url, method, path, etag string, body any, cookie *http.Cookie) (result, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, e := http.NewRequest(method, url+path, bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	data, e := io.ReadAll(res.Body)
	if e != nil {
		t.Fatal(e)
	}
	return result{status: res.StatusCode, body: data}, res.Header.Get("ETag")
}
func TestR16ConfigurationRevisionAndApply(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("settings-member", nil, nil), admin)
	member := loginAs(t, srv, "settings-member")
	r, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	expect(t, r, 200)
	initialID := int64(object(t, r)["id"].(float64))
	if tag != configTag(initialID) || initialID < 1 {
		t.Fatal(tag)
	}
	expect(t, call(t, srv.URL, "GET", "/api/settings/schema", nil, member, origin), 403)
	expect(t, call(t, srv.URL, "PATCH", "/api/settings", map[string]any{"set": map[string]any{"postgres-result-max-rows": 2}}, member, origin), 403)
	patch := func(key string, value any) any { return map[string]any{"set": map[string]any{key: value}} }
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", "", patch("postgres-result-max-rows", 2), admin)
	expect(t, r, 428)
	for _, body := range []any{patch("unknown", 1), patch("postgres-result-max-rows", 0), patch("postgres-result-max-rows", true), patch("postgres-result-max-rows", "2x"), map[string]any{"set": map[string]any{"postgres-result-max-rows": 2}, "clearSecrets": []string{"mail-password"}}, map[string]any{"set": map[string]any{"postgres-result-max-rows": 2, "postgres-query-timeout-seconds": 21}}} {
		r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, body, admin)
		expect(t, r, 422)
	}
	unchanged, e := a.system.Configuration(ctx, 0)
	if e != nil || unchanged.ID != initialID || unchanged.Values["postgres-result-max-rows"] != 100000 {
		t.Fatal("failed batch changed configuration", e)
	}
	// Concurrent editors share one revision: exactly one wins, the other gets 409.
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for _, n := range []int{2, 3} {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			v, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, patch("postgres-result-max-rows", n), admin)
			statuses <- v.status
		}(n)
	}
	wg.Wait()
	close(statuses)
	successes, conflicts := 0, 0
	for status := range statuses {
		if status == 202 {
			successes++
		}
		if status == 409 {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal(successes, conflicts)
	}
	desired, e := a.system.Configuration(ctx, 0)
	if e != nil {
		t.Fatal(e)
	}
	// Real consumer ACK and failure are distinct from saving. Errors never echo secrets.
	_, e = a.system.ApplyConfiguration(ctx, "failed-node", func(system.Revision) error { return errors.New("secret-must-not-be-exposed") })
	if !errors.Is(e, system.ErrApplyFailed) {
		t.Fatal(e)
	}
	view, e := a.system.ConfigurationView(ctx, 0)
	if e != nil || view.State != "failed" || view.Nodes[0].Error == "secret-must-not-be-exposed" {
		t.Fatal(view, e)
	}
	r, _ = settingsRequest(t, srv.URL, "POST", "/api/settings/revisions/1/retry", configTag(1), nil, admin)
	expect(t, r, 409)
	r, _ = settingsRequest(t, srv.URL, "POST", "/api/settings/revisions/"+strconv.FormatInt(desired.ID, 10)+"/retry", configTag(desired.ID), nil, admin)
	expect(t, r, 202)
	_, e = a.system.ApplyConfiguration(ctx, "failed-node", func(system.Revision) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.appliedConfiguration(ctx); e != nil {
		t.Fatal(e)
	}
	view, e = a.system.ConfigurationView(ctx, 0)
	if e != nil || view.State != "effective" {
		t.Fatal(view, e)
	}
	// Stale nodes are shown offline; they must re-read/apply before new work.
	_, e = pool.Exec(ctx, `UPDATE configuration_nodes SET last_seen=now()-interval '1 minute'`)
	if e != nil {
		t.Fatal(e)
	}
	view, e = a.system.ConfigurationView(ctx, 0)
	if e != nil || view.State == "effective" {
		t.Fatal("offline nodes reported ready", view, e)
	}
	r, tag = settingsRequest(t, srv.URL, "PUT", "/api/admin/properties", "", map[string]any{"postgres-query-timeout-seconds": "2"}, admin)
	expect(t, r, 204)
	if tag == "" {
		t.Fatal("legacy ETag missing")
	}
	desired, e = a.system.Configuration(ctx, 0)
	if e != nil || !desired.Legacy {
		t.Fatal("legacy write not recorded", e)
	}
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(1), patch("postgres-query-timeout-seconds", 3), admin)
	expect(t, r, 409)
	var audit int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM operation_logs WHERE action='settings.update'`).Scan(&audit); e != nil || audit != 2 {
		t.Fatal("configuration audit", audit, e)
	}
}
