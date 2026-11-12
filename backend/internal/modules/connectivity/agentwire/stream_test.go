package agentwire

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestFramedHalfClose(t *testing.T) {
	a, b := net.Pipe()
	left, right := &Conn{Conn: a}, &Conn{Conn: b}
	defer left.Close()
	defer right.Close()
	_ = left.SetDeadline(time.Now().Add(3 * time.Second))
	_ = right.SetDeadline(time.Now().Add(3 * time.Second))
	done := make(chan error, 1)
	go func() {
		data, e := io.ReadAll(right)
		if e != nil {
			done <- e
			return
		}
		if string(data) != "request" {
			done <- ErrProtocol
			return
		}
		_, e = right.Write([]byte("response"))
		if e == nil {
			e = right.CloseWrite()
		}
		done <- e
	}()
	if _, e := left.Write([]byte("request")); e != nil {
		t.Fatal(e)
	}
	if e := left.CloseWrite(); e != nil {
		t.Fatal(e)
	}
	response, e := io.ReadAll(left)
	if e != nil || string(response) != "response" {
		t.Fatal(string(response), e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
