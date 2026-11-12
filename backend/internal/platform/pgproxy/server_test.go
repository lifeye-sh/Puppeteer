package pgproxy

import (
	"context"
	"github.com/jackc/pgx/v5/pgproto3"
	"net"
	"testing"
)

func TestCancellationCapability(t *testing.T) {
	s := &Server{}
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1000}
	calls := 0
	pid, key, release, err := s.register(addr, func(context.Context) error { calls++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	request := &pgproto3.CancelRequest{ProcessID: pid, SecretKey: append([]byte(nil), key...)}
	request.SecretKey[0] ^= 1
	s.cancel(context.Background(), addr, request)
	request.SecretKey = key
	s.cancel(context.Background(), &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}, request)
	if calls != 0 {
		t.Fatal("forged or cross-address cancellation accepted")
	}
	s.cancel(context.Background(), addr, request)
	if calls != 1 {
		t.Fatal("valid capability rejected")
	}
	release()
	s.cancel(context.Background(), addr, request)
	if calls != 1 {
		t.Fatal("expired capability accepted")
	}
}
