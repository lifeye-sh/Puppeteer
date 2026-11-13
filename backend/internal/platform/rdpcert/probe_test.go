package rdpcert

import (
	"context"
	"errors"
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

func TestNegotiationFailureDetails(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go func() {
		defer server.Close()
		io.ReadFull(server, make([]byte, 19))
		server.Write([]byte{3, 0, 0, 19, 14, 0xd0, 0, 0, 0, 0, 0, 3, 0, 8, 0, 5, 0, 0, 0})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Probe(ctx, client, "localhost")
	var failure *NegotiationFailure
	var stage *ProbeError
	if !errors.As(err, &failure) || failure.Code != 5 || !errors.As(err, &stage) || stage.Stage != "negotiation_response" {
		t.Fatalf("lost negotiation details: %v", err)
	}
}

func TestStandardRDPSecurityAndShortResponses(t *testing.T) {
	for size := 11; size < 19; size++ {
		client, server := net.Pipe()
		packet := make([]byte, size)
		packet[0] = 3
		packet[3] = byte(size)
		packet[4] = byte(size - 5)
		packet[5] = 0xd0
		go func() { defer server.Close(); io.ReadFull(server, make([]byte, 19)); server.Write(packet) }()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := Probe(ctx, client, "localhost")
		cancel()
		client.Close()
		if err == nil {
			t.Fatalf("accepted non TLS response length %d", size)
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
