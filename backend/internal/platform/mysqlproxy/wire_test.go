package mysqlproxy

import (
	"bytes"
	"context"
	"errors"
	"github.com/go-mysql-org/go-mysql/mysql"
	"net"
	"testing"
	"time"
)

func netPipe(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	a.SetDeadline(time.Now().Add(5 * time.Second))
	b.SetDeadline(time.Now().Add(5 * time.Second))
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}
func TestBoundedPacketFragments(t *testing.T) {
	for _, size := range []int{0, 1, 1024, packetFragment, packetFragment + 1} {
		payload := bytes.Repeat([]byte{'x'}, size)
		var wire bytes.Buffer
		sequence := byte(0)
		if e := writePacket(&wire, &sequence, payload); e != nil {
			t.Fatal(e)
		}
		sequence = 0
		actual, e := readPacket(&wire, &sequence, MaxPacketBytes)
		if e != nil || !bytes.Equal(payload, actual) {
			t.Fatal(size, e)
		}
	}
	for _, data := range [][]byte{{3, 0, 0, 0, 1, 2, 3}, {1, 0, 0, 1, 1}, {255, 255, 255, 0}} {
		sequence := byte(0)
		if _, e := readPacket(bytes.NewReader(data), &sequence, 2); e == nil {
			t.Fatal("length/sequence budget bypass")
		}
	}
}
func TestNullRowsAndMultipleResults(t *testing.T) {
	// A text row starting with SQL NULL is 0xfb, not LOCAL INFILE. It is valid
	// inside a result set, but never as the first response packet.
	upstream, peer := netPipe(t)
	downstream, client := netPipe(t)
	relay := Relay{Upstream: upstream, Downstream: downstream}
	finished := make(chan error, 1)
	go func() {
		up, down := byte(1), byte(1)
		rows, failed, e := relay.results(&up, &down)
		if rows != 1 || failed {
			finished <- ErrPacket
			return
		}
		finished <- e
	}()
	packets := [][]byte{{1}, {3, 'd', 'e', 'f'}, {0xfe, 0, 0, 2, 0}, {0xfb}, {0xfe, 0, 0, 10, 0}, {0, 0, 0, 2, 0, 0, 0}}
	go func() {
		sequence := byte(1)
		for _, packet := range packets {
			if e := writePacket(peer, &sequence, packet); e != nil {
				return
			}
		}
	}()
	sequence := byte(1)
	for _, packet := range packets {
		actual, e := readPacket(client, &sequence, 1024)
		if e != nil || !bytes.Equal(actual, packet) {
			t.Fatal(e, actual)
		}
	}
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
}

func TestFailedPrepareCloseKeepsConnection(t *testing.T) {
	downstream, client := netPipe(t)
	upstream, peer := netPipe(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	relay := Relay{Downstream: downstream, Upstream: upstream, Check: func(context.Context) error { return nil }, Validate: func(string) error { return errors.New("denied") }, Audit: func(context.Context, QueryEvent) error { return nil }}
	done := make(chan error, 1)
	go func() { done <- relay.Serve(ctx) }()
	seq := byte(0)
	if e := writePacket(client, &seq, append([]byte{mysql.COM_STMT_PREPARE}, []byte("DELETE FROM items")...)); e != nil {
		t.Fatal(e)
	}
	packet, e := readPacket(client, &seq, 1024)
	if e != nil || packet[0] != 0xff {
		t.Fatal("prepare must be denied", e)
	}
	seq = 0
	if e = writePacket(client, &seq, []byte{mysql.COM_STMT_CLOSE, 0, 0, 0, 0}); e != nil {
		t.Fatal(e)
	}
	go func() {
		s := byte(0)
		p, e := readPacket(peer, &s, 1024)
		if e != nil || !bytes.Equal(p, []byte{mysql.COM_PING}) {
			peer.Close()
			return
		}
		_ = writePacket(peer, &s, []byte{0, 0, 0, 2, 0, 0, 0})
	}()
	seq = 0
	if e = writePacket(client, &seq, []byte{mysql.COM_PING}); e != nil {
		t.Fatal(e)
	}
	packet, e = readPacket(client, &seq, 1024)
	if e != nil || packet[0] != 0 {
		t.Fatal("connection lost after close of failed prepare", e)
	}
	seq = 0
	_ = writePacket(client, &seq, []byte{mysql.COM_QUIT})
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}

func TestDMLAffectedRowsAndLostAcknowledgement(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "affected", true: "unknown"}[lost], func(t *testing.T) {
			downstream, c := netPipe(t)
			upstream, peer := netPipe(t)
			events := make(chan QueryEvent, 2)
			done := make(chan error, 1)
			r := Relay{Downstream: downstream, Upstream: upstream, Check: func(context.Context) error { return nil }, Validate: func(string) error { return nil }, Audit: func(_ context.Context, e QueryEvent) error { events <- e; return nil }}
			go func() { done <- r.Serve(context.Background()) }()
			go func() {
				s := byte(0)
				if _, e := readPacket(peer, &s, 1024); e != nil {
					return
				}
				if lost {
					peer.Close()
					return
				}
				_ = writePacket(peer, &s, []byte{0, 7, 0, 2, 0, 0, 0})
			}()
			s := byte(0)
			if e := writePacket(c, &s, append([]byte{mysql.COM_QUERY}, []byte("COMMIT")...)); e != nil {
				t.Fatal(e)
			}
			if !lost {
				if _, e := readPacket(c, &s, 1024); e != nil {
					t.Fatal(e)
				}
				s = 0
				_ = writePacket(c, &s, []byte{mysql.COM_QUIT})
			}
			first, last := <-events, <-events
			if first.Phase != "start" || last.Phase != "finish" || last.Unknown != lost || last.Failed != lost {
				t.Fatal(first, last)
			}
			if !lost && last.Rows != 7 {
				t.Fatal("affected row count lost", last.Rows)
			}
			if err := <-done; (err != nil) != lost {
				t.Fatal("transport result", err)
			}
		})
	}
}
