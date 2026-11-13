package application

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestGraphicsWriteSurvivesTemporaryBackpressure(t *testing.T) {
	payload := strings.Repeat("x", 2<<20)
	ready := make(chan struct{})
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			done <- err
			return
		}
		defer ws.Close()
		if tcp, ok := ws.UnderlyingConn().(*net.TCPConn); ok {
			_ = tcp.SetWriteBuffer(1024)
		}
		live := &liveSession{ws: ws, clientWriteTimeout: 30 * time.Second}
		close(ready)
		done <- live.send('7', payload)
	}))
	defer server.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	<-ready
	// Exceed the old three-second deadline while the small send buffer fills.
	time.Sleep(3500 * time.Millisecond)
	_ = client.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, data, err := client.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("7"+payload)) {
		t.Fatal("graphics payload corrupted")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestGraphicsNetworkReason(t *testing.T) {
	for _, op := range []string{"read", "write"} {
		if got := graphicsClientFailure(op, fmt.Errorf("wrapped: %w", context.DeadlineExceeded)); got != "client_"+op+"_timeout" {
			t.Fatal(got)
		}
	}
	if got := graphicsClientFailure("read", &websocket.CloseError{Code: websocket.CloseNormalClosure}); got != "client_disconnected" {
		t.Fatal(got)
	}
}
