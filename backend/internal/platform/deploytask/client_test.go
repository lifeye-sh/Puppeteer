package deploytask

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnixClientAuthenticationAndAllowlist(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "daemon.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("t", 64)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("missing token")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"generation":7}`))
	})}
	go server.Serve(l)
	defer server.Close()
	c := Client{Socket: socket, Token: token}
	status, body, err := c.Call(context.Background(), "GET", "/v1/status", nil)
	if err != nil || status != 200 || !strings.Contains(string(body), "7") {
		t.Fatal(status, string(body), err)
	}
	if _, _, err = c.Call(context.Background(), "POST", "/shell", nil); err == nil {
		t.Fatal("arbitrary path accepted")
	}
}
