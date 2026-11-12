package application

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"next-terminal/backend/internal/modules/session"
	"strings"
	"testing"
)

func TestR16StatisticsScopeAndCalendar(t *testing.T) {
	pool, _, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	u := postID(t, srv, "/api/admin/users", userInput("stats-member", nil, nil), admin)
	v := postID(t, srv, "/api/admin/users", userInput("stats-other", nil, nil), admin)
	member := loginAs(t, srv, "stats-member")
	cred := postID(t, srv, "/api/admin/credentials", credInput("stats-credential"), admin)
	group := postID(t, srv, "/api/admin/resource-groups", map[string]any{"name": "stats-group"}, admin)
	asset := postID(t, srv, "/api/admin/assets", assetInput("stats-ssh", cred, group), admin)
	other := assetInput("stats-vnc", cred, "")
	other["protocol"] = "vnc"
	other["port"] = 5900
	postID(t, srv, "/api/admin/assets", other, admin)
	grant := postID(t, srv, "/api/admin/grants", map[string]any{"userId": u, "resourceId": group}, admin)
	// More than one listing page, a timezone boundary, and a purged record.
	_, err := pool.Exec(ctx, `INSERT INTO sessions(actor_id,asset_id,asset_version,credential_version,host_key_version,readonly,state,ticket_hash,auth_hash,ticket_expires_at,lease_expires_at,created_at,connected_at,closed_at,protocol)
	 SELECT CASE WHEN n<=25 THEN $1::uuid ELSE $2::uuid END,$3,1,1,1,false,CASE WHEN n=26 THEN 'failed' ELSE 'closed' END,'','',now(),now(),
	 '2026-01-01 16:30:00+00'::timestamptz,CASE WHEN n=26 THEN NULL ELSE '2026-01-01 16:30:10+00'::timestamptz END,'2026-01-01 16:31:10+00'::timestamptz,
	 CASE WHEN n=26 THEN 'vnc' ELSE 'ssh' END FROM generate_series(1,27) n`, u, v, asset)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE sessions SET audit_purged_at=now() WHERE id=(SELECT id FROM sessions WHERE actor_id=$1 AND protocol='ssh' LIMIT 1)`, v); err != nil {
		t.Fatal(err)
	}
	path := "/api/statistics/sessions?from=2026-01-01&to=2026-01-03&timezone=Asia%2FShanghai"
	read := func(path string, c *http.Cookie) session.Statistics {
		t.Helper()
		r := call(t, srv.URL, "GET", path, nil, c, origin)
		expect(t, r, 200)
		var out session.Statistics
		if err := json.Unmarshal(r.body, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	all := read(path+"&all=true", admin)
	if all.Totals.Sessions != 26 || all.Totals.Users != 2 || all.Totals.Failed != 1 || all.Totals.Seconds != 1500 || len(all.Days) != 3 || all.Days[0].Sessions != 0 || all.Days[1].Sessions != 26 || all.Days[2].Sessions != 0 || len(all.Protocols) != 2 {
		t.Fatalf("incorrect aggregate: %+v", all)
	}
	own := read(path, member)
	if own.Scope != "own" || own.Totals.Sessions != 25 || own.Totals.Users != 1 || own.Totals.Failed != 0 {
		t.Fatalf("scope leaked: %+v", own)
	}
	utc := read(strings.Replace(path, "Asia%2FShanghai", "UTC", 1), member)
	if utc.Days[0].Sessions != 25 || utc.Days[1].Sessions != 0 {
		t.Fatal("timezone boundary incorrect", utc.Days)
	}
	empty := read("/api/statistics/sessions?from=2025-01-01&to=2025-01-01", member)
	if empty.Totals.Sessions != 0 || len(empty.Days) != 1 || len(empty.Protocols) != 0 {
		t.Fatal(empty)
	}
	expect(t, call(t, srv.URL, "GET", path+"&all=true", nil, member, origin), 403)
	expect(t, call(t, srv.URL, "GET", path, nil, nil, origin), 401)
	for _, q := range []string{"from=2026-01-02&to=2026-01-01", "from=2025-01-01&to=2026-01-02", "timezone=not-a-zone", "timezone=Local", "from=bad", "format=jsonp"} {
		expect(t, call(t, srv.URL, "GET", "/api/statistics/sessions?"+q, nil, admin, origin), 422)
	}
	// DST changes must not add or remove a calendar date.
	dst := read("/api/statistics/sessions?from=2026-03-07&to=2026-03-09&timezone=America%2FNew_York", member)
	if len(dst.Days) != 3 || dst.Days[2].Date != "2026-03-09" {
		t.Fatal(dst)
	}
	csvResult := call(t, srv.URL, "GET", path+"&format=csv", nil, member, origin)
	expect(t, csvResult, 200)
	records, err := csv.NewReader(strings.NewReader(string(csvResult.body))).ReadAll()
	if err != nil || len(records) != 4 || records[2][1] != "25" {
		t.Fatal(records, err)
	}
	count := func(c *http.Cookie, query string, want int) {
		t.Helper()
		r := call(t, srv.URL, "GET", "/api/statistics/assets"+query, nil, c, origin)
		expect(t, r, 200)
		if got := len(object(t, r)["protocols"].([]any)); got != want {
			t.Fatal("asset visibility", got, want)
		}
	}
	count(admin, "?all=true", 2)
	count(member, "", 1)
	expect(t, call(t, srv.URL, "GET", "/api/statistics/assets?all=true", nil, member, origin), 403)
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/grants/"+grant, nil, admin, origin), 204)
	count(member, "", 0)
	// Audit history remains owned by its actor after asset grant revocation.
	if read(path, member).Totals.Sessions != 25 {
		t.Fatal("revocation erased owned history")
	}
}
