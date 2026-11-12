package connectivity

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/hashicorp/yamux"
	"net"
	"next-terminal/backend/internal/modules/connectivity/agentwire"
	"next-terminal/backend/internal/platform/fault"
	"sync"
	"time"
)

var ErrAgentOffline = errors.New("agent offline or dial capability unavailable")

func (c *agentChannel) send(v agentwire.Control) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.control.SetWriteDeadline(time.Now().Add(3 * time.Second))
	return agentwire.WriteJSON(c.control, v)
}
func (s *Service) AgentDialReady(id string) bool {
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	c := s.agents.active[id]
	return c != nil && c.ready && c.dial && !c.mux.IsClosed()
}

// authorize rechecks the caller's current asset, identity and session grant. Its
// context must have a bounded deadline; error cancels the data stream immediately.
func (s *Service) DialAgent(life, dialCtx context.Context, id, leaseID, host string, port int, authorize func(context.Context) error) (net.Conn, error) {
	s.agents.mu.Lock()
	ch := s.agents.active[id]
	if ch == nil || !ch.ready || !ch.dial || ch.mux.IsClosed() {
		s.agents.mu.Unlock()
		return nil, ErrAgentOffline
	}
	s.agents.mu.Unlock()
	select {
	case ch.slots <- struct{}{}:
	default:
		return nil, fault.Conflict
	}
	ctx, cancel := context.WithCancel(life)
	var stream *yamux.Stream
	var streamMu sync.Mutex
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			cancel()
			streamMu.Lock()
			if stream != nil {
				stream.Close()
			}
			streamMu.Unlock()
			<-ch.slots
		})
	}
	sendLease := func() error {
		c, done := context.WithTimeout(ctx, time.Second)
		defer done()
		if e := s.CheckAgentEpoch(c, id, ch.epoch); e != nil {
			return e
		}
		if e := authorize(c); e != nil {
			return e
		}
		p, _ := json.Marshal(agentwire.Lease{SessionID: leaseID, Host: host, Port: port, ExpiresAt: time.Now().Add(10 * time.Second).UnixMilli()})
		return ch.send(agentwire.Control{V: 1, Type: "lease_update", Epoch: ch.epoch, Payload: p})
	}
	if e := sendLease(); e != nil {
		cleanup()
		return nil, e
	}
	go func() {
		tick := time.NewTicker(3 * time.Second)
		defer tick.Stop()
		defer cleanup()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch.mux.CloseChan():
				return
			case <-tick.C:
				if sendLease() != nil {
					return
				}
			}
		}
	}()
	// OpenStream is bounded by yamux StreamOpenTimeout. Do not spawn an unowned dial.
	opened, e := ch.mux.OpenStream()
	if e != nil {
		cleanup()
		return nil, e
	}
	streamMu.Lock()
	stream = opened
	streamMu.Unlock()
	if ctx.Err() != nil {
		opened.Close()
		cleanup()
		return nil, ctx.Err()
	}
	stopDial := context.AfterFunc(dialCtx, func() { opened.Close() })
	defer stopDial()
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := dialCtx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = opened.SetDeadline(deadline)
	d := agentwire.Dial{V: 1, Type: "dial", RequestID: leaseID, SessionID: leaseID, Epoch: ch.epoch, Host: host, Port: port, Deadline: deadline.UnixMilli(), LeaseExpiresAt: time.Now().Add(10 * time.Second).UnixMilli()}
	if e = agentwire.WriteJSON(opened, d); e != nil {
		cleanup()
		return nil, e
	}
	var result agentwire.DialResult
	if e = agentwire.ReadJSON(opened, &result); e != nil {
		cleanup()
		return nil, e
	}
	if result.Type != "dial_result" || result.RequestID != leaseID || !result.OK {
		cleanup()
		return nil, fault.Denied
	}
	_ = opened.SetDeadline(time.Time{})
	return &agentwire.Conn{Conn: opened, OnClose: cleanup}, nil
}
