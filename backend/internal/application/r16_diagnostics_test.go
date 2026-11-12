package application

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestR16DiagnosticStream(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("diagnostic-member", nil, nil), admin)
	member := loginAs(t, srv, "diagnostic-member")
	expect(t, call(t, srv.URL, "GET", "/api/admin/diagnostics/source", nil, member, origin), 403)
	source := object(t, call(t, srv.URL, "GET", "/api/admin/diagnostics/source", nil, admin, origin))
	if source["sourceIp"] != "127.0.0.1" || source["trustedProxyApplied"] != false {
		t.Fatal(source)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var connections atomic.Int32
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			connections.Add(1)
			_ = c.Close()
		}
	}()
	credential := postID(t, srv, "/api/admin/credentials", credInput("diagnostic fixture"), admin)
	body := assetInput("diagnostic fixture", credential, "")
	body["host"] = "127.0.0.1"
	body["port"] = listener.Addr().(*net.TCPAddr).Port
	id := postID(t, srv, "/api/admin/assets", body, admin)
	for _, input := range []map[string]any{{"assetId": id, "kind": "tcp", "count": 11}, {"assetId": id, "kind": "shell", "count": 1}} {
		expect(t, call(t, srv.URL, "POST", "/api/admin/diagnostics/stream", input, admin, origin), 422)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/diagnostics/stream", map[string]any{"assetId": id, "kind": "tcp", "count": 1, "host": "169.254.169.254"}, admin, origin), 400)
	expect(t, call(t, srv.URL, "POST", "/api/admin/diagnostics/stream", map[string]any{"assetId": id, "kind": "tcp", "count": 1}, member, origin), 403)
	stream := func(count int) *http.Response {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"assetId": id, "kind": "tcp", "count": count})
		r, _ := http.NewRequest("POST", srv.URL+"/api/admin/diagnostics/stream", strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.AddCookie(admin)
		response, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		if response.StatusCode != 200 {
			b, _ := io.ReadAll(response.Body)
			response.Body.Close()
			t.Fatalf("stream %d %s", response.StatusCode, b)
		}
		return response
	}
	response := stream(2)
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || strings.Count(string(data), "event: sample") != 2 || !strings.Contains(string(data), `"succeeded":2`) || connections.Load() != 2 {
		t.Fatal(string(data), connections.Load(), err)
	}
	response = stream(10)
	reader := bufio.NewReader(response.Body)
	for {
		line, e := reader.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		if strings.HasPrefix(line, "event: sample") {
			break
		}
	}
	response.Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for len(a.probeSlots) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(a.probeSlots) != 0 {
		t.Fatal("cancel did not release probe slot")
	}
	time.Sleep(1100 * time.Millisecond)
	if connections.Load() != 3 {
		t.Fatal("continued probing after cancellation", connections.Load())
	}
	var completed int
	if err = pool.QueryRow(context.Background(), `SELECT count(*) FROM operation_logs WHERE action='diagnostic.completed'`).Scan(&completed); err != nil || completed != 2 {
		t.Fatal(completed, err)
	}
	// Revoking a saved target between samples must stop before the next dial.
	response = stream(3)
	reader = bufio.NewReader(response.Body)
	for {
		line, e := reader.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		if strings.HasPrefix(line, "event: sample") {
			break
		}
	}
	if _, err = pool.Exec(context.Background(), `UPDATE resource_registry SET enabled=false,version=version+1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(reader)
	response.Body.Close()
	if err != nil || !strings.Contains(string(data), "event: error") || connections.Load() != 4 {
		t.Fatal(string(data), err, connections.Load())
	}
}
