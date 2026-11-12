//go:build linux

package rdpworker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

type controlFixture struct {
	consumed, prepared, renewed, finalized int
	ttl                                    time.Duration
	deny                                   bool
}

func (f *controlFixture) ConsumeTicket(_ context.Context, ticket string) (ControlReply, error) {
	f.consumed++
	if ticket != "test-ticket" {
		return ControlReply{}, errors.New("private diagnostic")
	}
	return ControlReply{SessionID: "server-selected-session", LeaseUntil: time.Now().Add(f.ttl)}, nil
}
func (f *controlFixture) PrepareTarget(_ context.Context, id string) (json.RawMessage, error) {
	f.prepared++
	if id != "server-selected-session" || f.deny {
		return nil, errors.New("private credential must never be returned")
	}
	return json.RawMessage(`{"host":"127.0.0.1","port":12345}`), nil
}
func (f *controlFixture) RenewSession(_ context.Context, id string) (time.Time, error) {
	f.renewed++
	return time.Now().Add(f.ttl), nil
}
func (f *controlFixture) Finalize(_ context.Context, id string) error { f.finalized++; return nil }
func (f *controlFixture) ReportDeviceIO(_ context.Context, _ string, _ json.RawMessage) error {
	return nil
}

func TestControlRPC(t *testing.T) {
	for _, mode := range []string{"complete", "before-ticket", "replay-id", "prepare-twice", "foreign-session", "backend-failure", "lease-expired", "oversized", "unknown-field"} {
		t.Run(mode, func(t *testing.T) {
			server, client := net.Pipe()
			defer client.Close()
			f := &controlFixture{ttl: time.Second, deny: mode == "backend-failure"}
			if mode == "lease-expired" {
				f.ttl = 40 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() { defer server.Close(); done <- serveAuthorized(context.Background(), server, f) }()
			client.SetDeadline(time.Now().Add(2 * time.Second))
			exchange := func(req ControlRequest) error {
				if e := controlWrite(client, req); e != nil {
					return e
				}
				var reply ControlReply
				return controlRead(client, &reply)
			}
			if mode == "oversized" {
				var h [4]byte
				binary.BigEndian.PutUint32(h[:], maxControlFrame+1)
				client.Write(h[:])
			} else if mode == "unknown-field" {
				controlWrite(client, map[string]any{"id": 1, "method": "ConsumeTicket", "ticket": "test-ticket", "host": "untrusted"})
			} else if mode == "before-ticket" {
				if exchange(ControlRequest{ID: 1, Method: "PrepareTarget"}) == nil {
					t.Fatal("unauthenticated target prepared")
				}
			} else {
				if e := exchange(ControlRequest{ID: 1, Method: "ConsumeTicket", Ticket: "test-ticket"}); e != nil {
					t.Fatal(e)
				}
				var e error
				switch mode {
				case "complete":
					for i, m := range []string{"PrepareTarget", "RenewSession", "Finalize"} {
						if e = exchange(ControlRequest{ID: uint64(i + 2), Method: m}); e != nil {
							t.Fatal(e)
						}
					}
				case "replay-id":
					e = exchange(ControlRequest{ID: 1, Method: "PrepareTarget"})
				case "prepare-twice":
					if e = exchange(ControlRequest{ID: 2, Method: "PrepareTarget"}); e != nil {
						t.Fatal(e)
					}
					e = exchange(ControlRequest{ID: 3, Method: "PrepareTarget"})
				case "foreign-session":
					e = exchange(ControlRequest{ID: 2, Method: "RenewSession", Ticket: "foreign-ticket"})
				case "backend-failure":
					e = exchange(ControlRequest{ID: 2, Method: "PrepareTarget"})
				case "lease-expired":
					var reply ControlReply
					e = controlRead(client, &reply)
				}
				if mode != "complete" && e == nil {
					t.Fatal("invalid flow accepted")
				}
			}
			select {
			case e := <-done:
				if (mode == "complete") != (e == nil) {
					t.Fatalf("%s: %v", mode, e)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("control handler leaked")
			}
			if f.consumed > 0 && f.finalized != 1 {
				t.Fatalf("cleanup count %d", f.finalized)
			}
			if f.prepared > 1 {
				t.Fatal("target lease reused")
			}
		})
	}
}
