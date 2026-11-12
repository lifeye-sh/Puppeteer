package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestR10SnippetIsolationVersionAndAudit(t *testing.T) {
	p, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("snippet-a", nil, nil), admin)
	postID(t, srv, "/api/admin/users", userInput("snippet-b", nil, nil), admin)
	alice := loginAs(t, srv, "snippet-a")
	bob := loginAs(t, srv, "snippet-b")
	body := map[string]any{"name": "诊断", "content": "pwd\nprintf '%s' 'R10_MARKER'", "description": "只读检查"}
	id := postID(t, srv, "/api/snippets", body, alice)
	sys := postID(t, srv, "/api/admin/snippets", body, admin)
	expect(t, call(t, srv.URL, "POST", "/api/admin/snippets", body, alice, origin), 403)
	list := call(t, srv.URL, "GET", "/api/snippets", nil, bob, origin)
	expect(t, list, 200)
	if strings.Contains(string(list.body), id) || !strings.Contains(string(list.body), sys) {
		t.Fatal("personal snippet visibility leak", string(list.body))
	}
	body["version"] = 1
	expect(t, call(t, srv.URL, "PUT", "/api/snippets/"+id, body, bob, origin), 409)
	expect(t, call(t, srv.URL, "DELETE", "/api/snippets/"+id+"?version=1", nil, bob, origin), 409)
	expect(t, call(t, srv.URL, "PUT", "/api/snippets/"+sys, body, alice, origin), 409)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/snippets/"+id, body, admin, origin), 409)
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses <- call(t, srv.URL, "PUT", "/api/snippets/"+id, body, alice, origin).status
		}()
	}
	wg.Wait()
	close(statuses)
	success, conflict := 0, 0
	for v := range statuses {
		if v == 200 {
			success++
		}
		if v == 409 {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent writes: %d success %d conflict", success, conflict)
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/snippets/"+id+"?version=1", nil, alice, origin), 409)
	expect(t, call(t, srv.URL, "DELETE", "/api/snippets/"+id+"?version=2", nil, alice, origin), 204)
	var count int
	if e := p.QueryRow(context.Background(), `SELECT count(*) FROM operation_logs WHERE action LIKE 'snippet.%' AND details::text LIKE '%R10_MARKER%'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("snippet content leaked to operation audit", e, count)
	}
	if e := p.QueryRow(context.Background(), `SELECT count(*) FROM operation_logs WHERE action LIKE 'snippet.%'`).Scan(&count); e != nil || count != 4 {
		t.Fatal("mutation audit", e, count)
	}
	expect(t, call(t, srv.URL, "POST", "/api/snippets", map[string]string{"name": "bad", "content": strings.Repeat("中", 1366)}, alice, origin), 422)
	expect(t, call(t, srv.URL, "POST", "/api/snippets", map[string]string{"name": "bad", "content": "echo\x00"}, alice, origin), 422)
	expect(t, call(t, srv.URL, "GET", "/api/snippets?page=0", nil, alice, origin), 422)
	expect(t, call(t, srv.URL, "GET", "/api/snippets", nil, nil, origin), 401)
}

func TestR10SnippetSearchPagination(t *testing.T) {
	p, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	// Seed more than one page without spending API rate limits on fixture setup.
	for i := 0; i < 35; i++ {
		if _, e := p.Exec(context.Background(), `INSERT INTO snippets(name,content,description) VALUES($1,'pwd','linux')`, fmt.Sprintf("fixture-%02d", i)); e != nil {
			t.Fatal(e)
		}
	}
	first := object(t, call(t, srv.URL, "GET", "/api/snippets?scope=system", nil, admin, origin))
	second := object(t, call(t, srv.URL, "GET", "/api/snippets?scope=system&page=2", nil, admin, origin))
	if first["total"].(float64) != 35 || len(first["items"].([]any)) != 30 || len(second["items"].([]any)) != 5 {
		t.Fatal("pagination", first, second)
	}
	found := object(t, call(t, srv.URL, "GET", "/api/snippets?scope=system&search=TURE-01", nil, admin, origin))
	if found["total"].(float64) != 1 {
		t.Fatal("case insensitive search", found)
	}
	mine := object(t, call(t, srv.URL, "GET", "/api/snippets?scope=mine", nil, admin, origin))
	if mine["total"].(float64) != 0 {
		t.Fatal("scope", mine)
	}
}
