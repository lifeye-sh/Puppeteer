package deploytask

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPAuthenticationAndRequestValidation(t *testing.T) {
	e, err := Open(context.Background(), t.TempDir(), &fakeDriver{})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	h, err := Handler(context.Background(), e, token)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, auth, body string
		status                   int
	}{
		{"GET", "/v1/status", "", "", 401},
		{"GET", "/v1/status", "Bearer wrong", "", 401},
		{"GET", "/v1/status", "Bearer " + token, "", 200},
		{"POST", "/v1/tasks", "Bearer " + token, `{"protocol":"mysql","command":"rm"}`, 400},
		{"POST", "/v1/tasks", "Bearer " + token, `{"protocol":"mysql"} {}`, 400},
		{"POST", "/v1/tasks", "Bearer " + token, `{"protocol":"shell"}`, 422},
		{"POST", "/v1/tasks", "Bearer " + token, `{"protocol":"mysql","generation":42}`, 409},
		{"POST", "/v1/preflight", "Bearer " + token, `{"protocol":"mysql"}`, 200},
		{"POST", "/v1/tasks", "Bearer " + token, `{"protocol":"mysql"}`, 202},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", tc.auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	await(t, e)
}
func TestHTTPRejectsWeakToken(t *testing.T) {
	if _, err := Handler(context.Background(), nil, "short"); err == nil {
		t.Fatal("weak token accepted")
	}
}

func TestReadOnlyProgressTicket(t *testing.T) {
	e, err := Open(context.Background(), t.TempDir(), &fakeDriver{})
	if err != nil {
		t.Fatal(err)
	}
	task, err := e.Start(context.Background(), Request{Protocol: "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	await(t, e)
	h, err := Handler(context.Background(), e, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	check := func(method, path string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
		if want == 200 && (strings.Contains(w.Body.String(), task.ProgressToken) || strings.Contains(w.Body.String(), "request")) {
			t.Fatal("progress exposed private task fields")
		}
	}
	path := "/deployment-progress/" + task.ID + "?ticket=" + task.ProgressToken
	check("GET", path, 200)
	check("POST", path, 401)
	check("GET", "/deployment-progress/"+task.ID+"?ticket=wrong", 401)
	check("GET", "/v1/status?ticket="+task.ProgressToken, 401)
	e.mu.Lock()
	e.data.Task.Started = time.Now().Add(-3 * time.Hour)
	e.mu.Unlock()
	check("GET", path, 401)
}
