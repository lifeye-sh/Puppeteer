package agent

import (
	"context"
	"encoding/json"
	"github.com/hashicorp/yamux"
	"io"
	"net"
	"next-terminal/backend/internal/agentupdate"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/connectivity/agentwire"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type leaseEntry struct {
	value    agentwire.Lease
	deadline time.Time
	cancel   context.CancelFunc
	used     bool
}
type leases struct {
	mu      sync.Mutex
	items   map[string]*leaseEntry
	changed chan struct{}
}

func newLeases() *leases {
	return &leases{items: map[string]*leaseEntry{}, changed: make(chan struct{})}
}
func (l *leases) update(v agentwire.Lease) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	remaining := time.Until(time.UnixMilli(v.ExpiresAt))
	if remaining <= 0 || remaining > 15*time.Second || v.SessionID == "" || len(v.SessionID) > 128 || v.Host == "" || len(v.Host) > 253 || v.Port < 1 || v.Port > 65535 {
		return agentwire.ErrProtocol
	}
	old := l.items[v.SessionID]
	if old != nil {
		if old.value.Host != v.Host || old.value.Port != v.Port {
			return agentwire.ErrProtocol
		}
		old.value = v
		old.deadline = time.Now().Add(remaining)
	} else {
		if len(l.items) >= 64 {
			return agentwire.ErrProtocol
		}
		l.items[v.SessionID] = &leaseEntry{value: v, deadline: time.Now().Add(remaining)}
	}
	close(l.changed)
	l.changed = make(chan struct{})
	return nil
}
func (l *leases) close(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if v := l.items[id]; v != nil {
		if v.cancel != nil {
			v.cancel()
		}
		delete(l.items, id)
	}
}
func (l *leases) watch(ctx context.Context) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	defer func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		for _, v := range l.items {
			if v.cancel != nil {
				v.cancel()
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			l.mu.Lock()
			for id, v := range l.items {
				if time.Now().After(v.deadline) {
					if v.cancel != nil {
						v.cancel()
					}
					delete(l.items, id)
				}
			}
			l.mu.Unlock()
		}
	}
}
func (l *leases) claim(ctx context.Context, d agentwire.Dial, cancel context.CancelFunc) bool {
	for {
		l.mu.Lock()
		v := l.items[d.SessionID]
		if v != nil {
			ok := !v.used && v.value.Host == d.Host && v.value.Port == d.Port && time.Now().Before(v.deadline)
			if ok {
				v.used = true
				v.cancel = cancel
			}
			l.mu.Unlock()
			return ok
		}
		changed := l.changed
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-changed:
		}
	}
}
func serveDial(parent context.Context, stream *yamux.Stream, epoch int64, scope connectivity.NetworkScope, l *leases) {
	defer stream.Close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { stream.Close() })
	defer stop()
	_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
	var d agentwire.Dial
	if agentwire.ReadJSON(stream, &d) != nil || d.V != 1 || d.Type != "dial" || d.Epoch != epoch || len(d.RequestID) > 128 {
		return
	}
	deadline := time.UnixMilli(d.Deadline)
	if time.Until(deadline) <= 0 || time.Until(deadline) > 10*time.Second {
		return
	}
	dialCtx, done := context.WithDeadline(ctx, deadline)
	defer done()
	if !l.claim(dialCtx, d, cancel) {
		_ = agentwire.WriteJSON(stream, agentwire.DialResult{Type: "dial_result", RequestID: d.RequestID, ErrorCode: "lease_denied"})
		return
	}
	defer l.close(d.SessionID)
	target, ip, e := scope.DialTarget(dialCtx, d.Host, d.Port)
	if e != nil {
		_ = agentwire.WriteJSON(stream, agentwire.DialResult{Type: "dial_result", RequestID: d.RequestID, ErrorCode: "target_denied_or_unreachable"})
		return
	}
	defer target.Close()
	stopTarget := context.AfterFunc(ctx, func() { target.Close() })
	defer stopTarget()
	if agentwire.WriteJSON(stream, agentwire.DialResult{Type: "dial_result", RequestID: d.RequestID, OK: true, ResolvedIP: ip}) != nil {
		return
	}
	_ = stream.SetDeadline(time.Time{})
	framed := &agentwire.Conn{Conn: stream}
	completed := make(chan error, 2)
	go func() {
		_, e := io.Copy(target, framed)
		if e == nil {
			e = target.(*net.TCPConn).CloseWrite()
		}
		completed <- e
	}()
	go func() {
		_, e := io.Copy(framed, target)
		if e == nil {
			e = framed.CloseWrite()
		}
		completed <- e
	}()
	if e := <-completed; e != nil {
		cancel()
	}
	<-completed
}
func controlLoop(ctx context.Context, mux *yamux.Session, control *yamux.Stream, epoch int64, scope connectivity.NetworkScope, config Config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	l := newLeases()
	go l.watch(ctx)
	slots := make(chan struct{}, 64)
	var draining atomic.Bool
	go func() {
		for {
			stream, e := mux.AcceptStream()
			if e != nil {
				return
			}
			if draining.Load() {
				stream.Close()
				continue
			}
			select {
			case slots <- struct{}{}:
				go func() { defer func() { <-slots }(); serveDial(ctx, stream, epoch, scope, l) }()
			default:
				stream.Close()
			}
		}
	}()
	var writeMu sync.Mutex
	send := func(v agentwire.Control) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = control.SetWriteDeadline(time.Now().Add(15 * time.Second))
		return agentwire.WriteJSON(control, v)
	}
	stop := context.AfterFunc(ctx, func() { mux.Close() })
	defer stop()
	go func() {
		tick := time.NewTicker(time.Duration(config.HeartbeatSeconds) * time.Second)
		defer tick.Stop()
		for {
			if send(agentwire.Control{V: 1, Type: "heartbeat", ID: "heartbeat", Epoch: epoch}) != nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	go func() {
		seconds := config.CollectionSeconds
		if seconds < 5 || seconds > 300 {
			seconds = 5
		}
		tick := time.NewTicker(time.Duration(seconds) * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				c, done := context.WithTimeout(ctx, 3*time.Second)
				v := collectMetrics(c, config.DiskPaths, config.NetworkPattern)
				v.IntervalSeconds = seconds
				if config.Updates != nil {
					if raw, e := os.ReadFile(filepath.Join(config.Updates.StateDir, "status.json")); e == nil && len(raw) < 8192 {
						_ = json.Unmarshal(raw, &v.Update)
					}
				}
				done()
				raw, _ := json.Marshal(v)
				if send(agentwire.Control{V: 1, Type: "metrics", Epoch: epoch, Payload: raw}) != nil {
					cancel()
					return
				}
			}
		}
	}()
	if config.Updates != nil {
		go func() {
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					if _, e := os.Stat(filepath.Join(config.Updates.StateDir, "drain.json")); e == nil && !draining.Load() {
						if send(agentwire.Control{V: 1, Type: "drain", Epoch: epoch}) != nil {
							cancel()
							return
						}
						draining.Store(true)
					}
					if draining.Load() && len(slots) == 0 {
						cancel()
						return
					}
				}
			}
		}()
	}
	lastAck := time.Now()
	for {
		_ = control.SetReadDeadline(lastAck.Add(time.Duration(config.HeartbeatSeconds) * time.Second * 3 / 2))
		var msg agentwire.Control
		if e := agentwire.ReadJSON(control, &msg); e != nil {
			if draining.Load() && len(slots) == 0 {
				return ErrUpdate
			}
			return e
		}
		if msg.V != 1 || msg.Epoch != epoch {
			return agentwire.ErrProtocol
		}
		switch msg.Type {
		case "heartbeat_ack":
			var ack struct {
				Rotate bool   `json:"rotate"`
				Update string `json:"update"`
			}
			if len(msg.Payload) > 0 && json.Unmarshal(msg.Payload, &ack) != nil {
				return agentwire.ErrProtocol
			}
			if config.Updates != nil && (ack.Update == "check" || ack.Update == "install") {
				if e := agentupdate.WriteJSON(filepath.Join(config.Updates.StateDir, "request.json"), map[string]string{"action": ack.Update}); e != nil {
					return e
				}
			}
			if ack.Rotate {
				return ErrRotate
			}
			lastAck = time.Now()
		case "lease_update":
			var v agentwire.Lease
			if json.Unmarshal(msg.Payload, &v) != nil {
				return agentwire.ErrProtocol
			}
			if e := l.update(v); e != nil {
				return e
			}
		case "close_session":
			var v agentwire.Lease
			if json.Unmarshal(msg.Payload, &v) != nil {
				return agentwire.ErrProtocol
			}
			l.close(v.SessionID)
		default:
			return agentwire.ErrProtocol
		}
	}
}
