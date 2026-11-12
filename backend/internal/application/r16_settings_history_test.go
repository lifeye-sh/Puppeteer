package application

import (
	"context"
	"encoding/json"
	"fmt"
	"next-terminal/backend/internal/modules/system"
	"strings"
	"testing"
)

func TestR16ConfigurationHistoryAndDiff(t *testing.T) {
	pool, _, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("history-member", nil, nil), admin)
	member := loginAs(t, srv, "history-member")
	role := postID(t, srv, "/api/admin/roles", map[string]any{"name": "settings-reader", "permissions": []string{"settings.read"}}, admin)
	postID(t, srv, "/api/admin/users", userInput("history-reader", []string{role}, nil), admin)
	reader := loginAs(t, srv, "history-reader")
	for _, path := range []string{"/api/settings/revisions", "/api/settings/revisions/1/diff"} {
		expect(t, call(t, srv.URL, "GET", path, nil, reader, origin), 200)
	}
	expect(t, call(t, srv.URL, "PATCH", "/api/settings", map[string]any{"set": map[string]any{"system-name": "forbidden"}}, reader, origin), 403)
	for _, path := range []string{"/api/settings/revisions", "/api/settings/revisions/1/diff"} {
		expect(t, call(t, srv.URL, "GET", path, nil, nil, origin), 401)
		expect(t, call(t, srv.URL, "GET", path, nil, member, origin), 403)
	}
	read := func(path string) result { return call(t, srv.URL, "GET", path, nil, admin, origin) }
	current := read("/api/settings")
	expect(t, current, 200)
	initial := int64(object(t, current)["id"].(float64))
	head := initial
	save := func(values map[string]any) int64 {
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(head), map[string]any{"set": values}, admin)
		expect(t, r, 202)
		head = int64(object(t, r)["id"].(float64))
		return head
	}
	first := save(map[string]any{"system-name": "History test", "login-lock-enabled": false, "login-lock-ip-duration": 1})
	second := save(map[string]any{"system-name": "History second", "login-lock-enabled": true, "login-lock-ip-duration": 0})
	list := func(path string) system.RevisionPage {
		r := read(path)
		expect(t, r, 200)
		if strings.Contains(string(r.body), "History second") || strings.Contains(string(r.body), "snapshot") || strings.Contains(string(r.body), "\"values\"") {
			t.Fatal("history leaked snapshot", string(r.body))
		}
		var v system.RevisionPage
		if e := json.Unmarshal(r.body, &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	page := list("/api/settings/revisions?limit=2")
	if len(page.Items) != 2 || page.Items[0].ID != second || page.Items[0].PreviousID != first || page.Items[0].Actor != "accept-admin" || page.CurrentID != second || !page.HasMore || page.NextBefore != first {
		t.Fatal(page)
	}
	save(map[string]any{"system-name": "New concurrent revision"})
	older := list(fmt.Sprintf("/api/settings/revisions?limit=2&before=%d", page.NextBefore))
	if len(older.Items) != 2 || older.Items[0].ID != initial {
		t.Fatal("pagination displaced after concurrent save", older)
	}
	diff := func(path string) system.ConfigurationDiff {
		r := read(path)
		expect(t, r, 200)
		var v system.ConfigurationDiff
		if e := json.Unmarshal(r.body, &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	v := diff(fmt.Sprintf("/api/settings/revisions/%d/diff", second))
	if v.FromID != first || v.ToID != second || len(v.Changes) != 3 {
		t.Fatal(v)
	}
	found := map[string]system.ConfigurationChange{}
	for _, c := range v.Changes {
		found[c.Key] = c
		if !c.BeforePresent || !c.AfterPresent {
			t.Fatal(c)
		}
	}
	if found["login-lock-enabled"].Before != false || found["login-lock-enabled"].After != true || found["login-lock-ip-duration"].After != float64(0) {
		t.Fatal(found)
	}
	if len(diff(fmt.Sprintf("/api/settings/revisions/%d/diff?from=%d", head, head)).Changes) != 0 {
		t.Fatal("same revision differs")
	}
	firstPage := diff("/api/settings/revisions/1/diff")
	if firstPage.FromID != 0 || len(firstPage.Changes) == 0 {
		t.Fatal(firstPage)
	}
	for _, c := range firstPage.Changes {
		if c.BeforePresent || !c.AfterPresent {
			t.Fatal(c)
		}
	}
	// Unknown historical values must never be exposed through the diff allowlist.
	if _, e := pool.Exec(ctx, `UPDATE configuration_revisions SET snapshot=snapshot || '{"historic-secret":"must-not-appear"}'::jsonb WHERE id=$1`, first); e != nil {
		t.Fatal(e)
	}
	secretDiff := read(fmt.Sprintf("/api/settings/revisions/%d/diff?from=0", first))
	expect(t, secretDiff, 200)
	if strings.Contains(string(secretDiff.body), "historic-secret") || strings.Contains(string(secretDiff.body), "must-not-appear") {
		t.Fatal("unknown field leaked")
	}
	for _, path := range []string{"?before=-1", "?before=01", "?before=1&before=2", "?before=", "?limit=0", "?limit=51", "?limit=1&limit=2", "/01/diff", "/0/diff", "/1/diff?from=-1", "/1/diff?from=01", "/1/diff?from=0&from=1", "/1/diff?from="} {
		expect(t, read("/api/settings/revisions"+path), 422)
	}
	expect(t, read("/api/settings/revisions/999999/diff"), 404)
	expect(t, read("/api/settings/revisions/1/diff?from=999999"), 404)
	var after int64
	if e := pool.QueryRow(ctx, `SELECT revision FROM configuration_head WHERE singleton=1`).Scan(&after); e != nil || after != head {
		t.Fatal("read changed head", after, e)
	}
}
