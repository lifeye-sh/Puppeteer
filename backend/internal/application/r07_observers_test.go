package application

import (
	"context"
	"encoding/json"
	"net/http"
	"next-terminal/backend/internal/modules/audit"
	"strings"
	"testing"
)

func TestR07ObserverSharesScopeRevocationAndReplay(t *testing.T) {
	_, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	recipient := postID(t, srv, "/api/admin/users", userInput("observer-user", nil, nil), admin)
	postID(t, srv, "/api/admin/users", userInput("observer-wrong", nil, nil), admin)
	viewer := loginAs(t, srv, "observer-user")
	wrong := loginAs(t, srv, "observer-wrong")
	cred := postID(t, srv, "/api/admin/credentials", credInput("observer credential"), admin)
	assetBody := assetInput("observe Telnet", cred, "")
	assetBody["protocol"] = "telnet"
	asset := postID(t, srv, "/api/admin/assets", assetBody, admin)
	id, _ := reserve(t, srv, admin, asset, false)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := a.db.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`UPDATE sessions SET state='connected',lease_expires_at=now()+interval '5 minutes' WHERE id=$1`, id)
	if e := a.audit.Record(ctx, id, "output", 10, []byte("observer-visible")); e != nil {
		t.Fatal(e)
	}
	prefix := "/api/sessions/" + id
	shareBody := map[string]any{"username": "observer-user", "minutes": 10}
	expect(t, call(t, srv.URL, "POST", prefix+"/shares", shareBody, admin, origin), 403)
	grant := postID(t, srv, "/api/admin/grants", map[string]any{"userId": recipient, "resourceId": asset}, admin)
	expect(t, call(t, srv.URL, "POST", prefix+"/observers", map[string]string{}, viewer, origin), 403)
	create := func() (string, string) {
		t.Helper()
		r := call(t, srv.URL, "POST", prefix+"/shares", shareBody, admin, origin)
		expect(t, r, 200)
		v := object(t, r)
		path := v["path"].(string)
		ticket := strings.Split(path, "#ticket=")[1]
		var stored string
		if e := a.db.Row(ctx, `SELECT token_hash FROM session_shares WHERE id=$1`, v["id"]).Scan(&stored); e != nil || stored == ticket || len(stored) != 64 {
			t.Fatal("token storage")
		}
		return v["id"].(string), ticket
	}
	join := func(c *http.Cookie, ticket string) string {
		t.Helper()
		return postID(t, srv, prefix+"/observers", map[string]string{"ticket": ticket}, c)
	}
	close := func(observer string, c *http.Cookie) {
		t.Helper()
		expect(t, call(t, srv.URL, "DELETE", prefix+"/observers/"+observer, nil, c, origin), 204)
	}
	share, ticket := create()
	expect(t, call(t, srv.URL, "POST", prefix+"/observers", map[string]string{"ticket": ticket}, wrong, origin), 403)
	observer := join(viewer, ticket)
	events := prefix + "/observers/" + observer + "/events?after=0"
	expect(t, call(t, srv.URL, "POST", prefix+"/observers", map[string]string{"ticket": ticket}, viewer, origin), 403)
	r := call(t, srv.URL, "GET", events, nil, viewer, "")
	expect(t, r, 200)
	var page audit.RecordingPage
	if e := json.Unmarshal(r.body, &page); e != nil {
		t.Fatal(e)
	}
	if page.State != "writing" || len(page.Events) != 1 || string(page.Events[0].Payload) != "observer-visible" {
		t.Fatal("live durable prefix missing")
	}
	expect(t, call(t, srv.URL, "GET", events, nil, wrong, ""), 403)
	otherLogin := loginAs(t, srv, "observer-user")
	expect(t, call(t, srv.URL, "GET", events, nil, otherLogin, ""), 403)
	expect(t, call(t, srv.URL, "POST", prefix+"/commands", map[string]string{"command": "not allowed"}, viewer, origin), 403)
	expect(t, call(t, srv.URL, "POST", prefix+"/terminate", nil, viewer, origin), 403)
	expect(t, call(t, srv.URL, "DELETE", prefix+"/shares/"+share, nil, viewer, origin), 403)
	expect(t, call(t, srv.URL, "DELETE", prefix+"/shares/"+share, nil, admin, origin), 204)
	expect(t, call(t, srv.URL, "GET", events, nil, viewer, ""), 403)
	close(observer, viewer)
	_, ticket = create()
	observer = join(viewer, ticket)
	events = prefix + "/observers/" + observer + "/events"
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/grants/"+grant, nil, admin, origin), 204)
	expect(t, call(t, srv.URL, "GET", events, nil, viewer, ""), 403)
	close(observer, viewer)
	postID(t, srv, "/api/admin/grants", map[string]any{"userId": recipient, "resourceId": asset}, admin)
	share, ticket = create()
	exec(`UPDATE session_shares SET expires_at=now()-interval '1 second' WHERE id=$1`, share)
	expect(t, call(t, srv.URL, "POST", prefix+"/observers", map[string]string{"ticket": ticket}, viewer, origin), 403)
	adminObserver := join(admin, "")
	events = prefix + "/observers/" + adminObserver + "/events"
	expect(t, call(t, srv.URL, "GET", events+"?after=999", nil, admin, ""), 422)
	expect(t, call(t, srv.URL, "GET", events, nil, admin, ""), 200)
	exec(`UPDATE session_observers SET heartbeat_expires_at=now()-interval '1 second' WHERE id=$1`, adminObserver)
	expect(t, call(t, srv.URL, "GET", events, nil, admin, ""), 403)
	adminObserver = join(admin, "")
	events = prefix + "/observers/" + adminObserver + "/events"
	exec(`UPDATE sessions SET closed_at=now(),state='closed' WHERE id=$1`, id)
	exec(`UPDATE recordings SET state='complete' WHERE session_id=$1`, id)
	r = call(t, srv.URL, "GET", events, nil, admin, "")
	expect(t, r, 200)
	if !strings.Contains(string(r.body), "complete") {
		t.Fatal("end not signaled")
	}
	expect(t, call(t, srv.URL, "POST", prefix+"/observers", map[string]string{}, admin, origin), 409)
	close(adminObserver, admin)
}
