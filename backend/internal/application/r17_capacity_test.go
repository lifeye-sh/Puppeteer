package application

import (
	"context"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"testing"
	"time"
)

// A bounded deployment baseline, not a universal sizing or maximum-throughput claim.
func TestR17SustainedSessionCapacity(t *testing.T) {
	if testing.Short() {
		t.Skip("sustained workload")
	}
	host, port, _ := sshTarget(t)
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	const count = 16
	users := make([]*http.Cookie, 4)
	for i := range users {
		name := fmt.Sprintf("capacity-%d", i)
		uid := postID(t, srv, "/api/admin/users", userInput(name, nil, nil), admin)
		postID(t, srv, "/api/admin/grants", map[string]any{"userId": uid, "resourceId": asset}, admin)
		users[i] = loginAs(t, srv, name)
	}
	ids := make([]string, count)
	sockets := make([]*websocket.Conn, count)
	for i := range ids {
		id, ticket := reserve(t, srv, users[i%4], asset, false)
		ids[i] = id
		sockets[i] = attach(t, srv, users[i%4], id, ticket)
		readUntil(t, sockets[i], "3"+id)
	}
	expect(t, call(t, srv.URL, "POST", "/api/sessions", map[string]any{"assetId": asset}, admin, origin), 409)
	start := time.Now()
	operations := 0
	for time.Since(start) < 2*time.Minute {
		for i, ws := range sockets {
			command := fmt.Sprintf("round-%d-node-%d", operations, i)
			if e := ws.WriteMessage(websocket.TextMessage, []byte("1"+command+"\n")); e != nil {
				t.Fatal(e)
			}
			readUntil(t, ws, "EXECUTED "+command)
			operations++
		}
		expect(t, call(t, srv.URL, "GET", "/api/sessions?all=true", nil, admin, origin), 200)
		time.Sleep(2 * time.Second)
	}
	for i, id := range ids {
		expect(t, call(t, srv.URL, "POST", "/api/sessions/"+id+"/terminate", nil, users[i%4], origin), 204)
		waitClosed(t, a, id)
		expect(t, call(t, srv.URL, "GET", "/api/sessions/"+id+"/recording", nil, users[i%4], origin), 200)
	}
	var recorded int
	if e := p.QueryRow(context.Background(), `SELECT count(DISTINCT session_id) FROM recording_chunks WHERE session_id=ANY($1::uuid[])`, ids).Scan(&recorded); e != nil || recorded != count {
		t.Fatal(recorded, e)
	}
	// A new connection after load verifies the service releases leases and remains usable.
	id, ticket := reserve(t, srv, admin, asset, false)
	ws := attach(t, srv, admin, id, ticket)
	readUntil(t, ws, "3"+id)
	ws.Close()
	waitClosed(t, a, id)
	t.Logf("capacity baseline: sessions=%d commands=%d duration=%s recordingSessions=%d", count, operations, time.Since(start), recorded)
}
