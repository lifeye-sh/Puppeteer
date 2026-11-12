package rdpcert

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestRejectInvalidNegotiation(t *testing.T) {
	for _, packet := range [][]byte{
		{3, 0, 0, 4}, {3, 0, 255, 255},
		{3, 0, 0, 19, 14, 0xd0, 0, 0, 0, 0, 0, 3, 0, 8, 0, 1, 0, 0, 0},
		{3, 0, 0, 19, 14, 0xd0, 0, 0, 0, 0, 0, 2, 0, 8, 0, 0, 0, 0, 0},
	} {
		client, server := net.Pipe()
		go func() { defer server.Close(); io.ReadFull(server, make([]byte, 19)); server.Write(packet) }()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := Probe(ctx, client, "localhost")
		cancel()
		client.Close()
		if err == nil {
			t.Fatal("accepted invalid negotiation")
		}
	}
}

func TestProbeCancellation(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := Probe(ctx, client, "localhost"); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("probe did not stop")
	}
}
