package connectivity

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
	"github.com/jackc/pgx/v5"
	"net/http"
	"next-terminal/backend/internal/modules/connectivity/agentwire"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"sync"
	"time"
)

type agentChannel struct {
	epoch   int64
	cancel  context.CancelFunc
	mux     *yamux.Session
	ready   bool
	control *yamux.Stream
	writeMu sync.Mutex
	dial    bool
	slots   chan struct{}
}
type agentChannels struct {
	mu       sync.Mutex
	active   map[string]*agentChannel
	stopping bool
}

func (s *Service) StopAgents() {
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	s.agents.stopping = true
	for _, v := range s.agents.active {
		v.cancel()
	}
}
func (s *Service) DisconnectAgent(id string) {
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	if v := s.agents.active[id]; v != nil {
		v.cancel()
	}
}
func (s *Service) AgentOnline(id string) bool {
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	v := s.agents.active[id]
	return v != nil && v.ready && !v.mux.IsClosed()
}
func (s *Service) ClaimAgent(ctx context.Context, key string) (id string, epoch int64, scope NetworkScope, e error) {
	if len(key) != 64 {
		e = fault.Denied
		return
	}
	var raw []byte
	e = s.db.Row(ctx, `UPDATE agent_gateway_configs SET connection_epoch=connection_epoch+1,last_connected_at=now() WHERE (credential_hash=$1 OR (previous_credential_hash=$1 AND previous_expires_at>now())) AND revoked_at IS NULL RETURNING id,connection_epoch,network_scope`, deviceDigest(key)).Scan(&id, &epoch, &raw)
	if errors.Is(e, pgx.ErrNoRows) {
		e = fault.Denied
	}
	if e == nil {
		e = json.Unmarshal(raw, &scope)
	}
	return
}
func (s *Service) CheckAgentEpoch(ctx context.Context, id string, epoch int64) error {
	var valid bool
	e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM agent_gateway_configs WHERE id=$1 AND connection_epoch=$2 AND revoked_at IS NULL)`, id, epoch).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return fault.Denied
	}
	return nil
}

// ServeAgent authenticates the control stream; data streams originate only at the server.
func (s *Service) ServeAgent(w http.ResponseWriter, r *http.Request, key string) error {
	found := false
	for _, v := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		if strings.TrimSpace(v) == agentwire.Protocol {
			found = true
		}
	}
	if !found {
		return fault.Invalid
	}
	id, epoch, scope, e := s.ClaimAgent(r.Context(), key)
	if e != nil {
		return e
	}
	ws, e := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{agentwire.Protocol}, CompressionMode: websocket.CompressionDisabled})
	if e != nil {
		return nil
	}
	defer ws.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	// NetConn disables message size limits; restore a bounded frame size afterward.
	ws.SetReadLimit(512 * 1024)
	mux, e := yamux.Server(conn, agentwire.MuxConfig())
	if e != nil {
		return nil
	}
	defer mux.Close()
	ch := &agentChannel{epoch: epoch, cancel: cancel, mux: mux, slots: make(chan struct{}, 64)}
	s.agents.mu.Lock()
	if s.agents.stopping || (s.agents.active[id] != nil && s.agents.active[id].epoch >= epoch) {
		s.agents.mu.Unlock()
		return nil
	}
	if old := s.agents.active[id]; old != nil {
		old.cancel()
	}
	s.agents.active[id] = ch
	s.agents.mu.Unlock()
	defer func() {
		s.agents.mu.Lock()
		if s.agents.active[id] == ch {
			delete(s.agents.active, id)
		}
		s.agents.mu.Unlock()
	}()
	// Cancel immediately wakes stalled reads/handshakes; no DB failure can extend identity.
	stop := context.AfterFunc(ctx, func() { _ = mux.Close(); _ = ws.CloseNow() })
	defer stop()
	guardDone := make(chan struct{})
	defer func() { cancel(); <-guardDone }()
	go func() {
		defer close(guardDone)
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				c, done := context.WithTimeout(ctx, time.Second)
				e := s.CheckAgentEpoch(c, id, epoch)
				var credentialOK bool
				if e == nil {
					e = s.db.Row(c, `SELECT EXISTS(SELECT 1 FROM agent_gateway_configs WHERE id=$1 AND (credential_hash=$2 OR (previous_credential_hash=$2 AND previous_expires_at>now())))`, id, deviceDigest(key)).Scan(&credentialOK)
					if e == nil && !credentialOK {
						e = fault.Denied
					}
				}
				done()
				if e != nil {
					cancel()
					return
				}
			}
		}
	}()
	handshake, done := context.WithTimeout(ctx, 10*time.Second)
	control, e := mux.AcceptStreamWithContext(handshake)
	done()
	if e != nil {
		return nil
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(10 * time.Second))
	var hello agentwire.Control
	if e = agentwire.ReadJSON(control, &hello); e != nil || hello.V != 1 || hello.Type != "hello" || hello.Epoch != 0 || len(hello.ID) > 128 {
		return nil
	}
	c, done := context.WithTimeout(ctx, time.Second)
	e = s.CheckAgentEpoch(c, id, epoch)
	done()
	if e != nil {
		return nil
	}
	var capabilities struct {
		Capabilities     []string `json:"capabilities"`
		HeartbeatSeconds int      `json:"heartbeatSeconds"`
		Version          string   `json:"version"`
	}
	_ = json.Unmarshal(hello.Payload, &capabilities)
	for _, capability := range capabilities.Capabilities {
		if capability == "dial.v1" {
			ch.dial = true
		}
	}
	if len(capabilities.Version) > 100 {
		return nil
	}
	if capabilities.Version != "" {
		if _, e = s.db.Exec(ctx, `UPDATE agent_gateway_configs SET version=$2 WHERE id=$1 AND connection_epoch=$3`, id, capabilities.Version, epoch); e != nil {
			return nil
		}
	}
	capsRaw, _ := json.Marshal(capabilities.Capabilities)
	if len(capabilities.Capabilities) > 32 {
		return nil
	}
	if _, e = s.db.Exec(ctx, `UPDATE agent_gateway_configs SET capabilities=$2 WHERE id=$1 AND connection_epoch=$3`, id, capsRaw, epoch); e != nil {
		return nil
	}
	heartbeat := capabilities.HeartbeatSeconds
	if heartbeat == 0 {
		heartbeat = 30
	}
	if heartbeat < 10 || heartbeat > 60 {
		return nil
	}
	ch.control = control
	if _, e = s.db.Exec(ctx, `UPDATE agent_gateway_configs SET previous_credential_hash=NULL,previous_expires_at=NULL WHERE id=$1 AND credential_hash=$2 AND connection_epoch=$3`, id, deviceDigest(key), epoch); e != nil {
		return nil
	}
	s.agents.mu.Lock()
	ch.ready = true
	s.agents.mu.Unlock()
	payload, _ := json.Marshal(map[string]any{"agentId": id, "networkScope": scope, "capabilities": []string{"heartbeat", "dial.v1"}, "heartbeatSeconds": heartbeat})
	if e = agentwire.WriteJSON(control, agentwire.Control{V: 1, Type: "hello_ack", ID: hello.ID, Epoch: epoch, Payload: payload}); e != nil {
		return nil
	}
	// Any additional agent-originated stream violates the current control-only contract.
	go func() {
		extra, e := mux.AcceptStream()
		if e == nil {
			_ = extra.Close()
			cancel()
		}
	}()
	lastHeartbeat := time.Now().Add(-time.Second)
	var lastMetrics time.Time
	for {
		_ = control.SetReadDeadline(lastHeartbeat.Add(time.Duration(heartbeat) * 3 * time.Second))
		var msg agentwire.Control
		if e = agentwire.ReadJSON(control, &msg); e != nil || msg.V != 1 || msg.Epoch != epoch || len(msg.ID) > 128 {
			return nil
		}
		if msg.Type == "drain" {
			s.agents.mu.Lock()
			ch.dial = false
			s.agents.mu.Unlock()
			continue
		}
		if msg.Type == "metrics" {
			if time.Since(lastMetrics) < 4*time.Second {
				return nil
			}
			lastMetrics = time.Now()
			var m agentwire.Metrics
			if json.Unmarshal(msg.Payload, &m) != nil || len(m.Disks) > 32 || len(m.Networks) > 128 || len(m.Errors) > 64 || m.MemoryUsed > m.MemoryTotal || (m.CPU != nil && (*m.CPU < 0 || *m.CPU > 100)) {
				return nil
			}
			c, done := context.WithTimeout(ctx, time.Second)
			_, e = s.db.Exec(c, `UPDATE agent_gateway_configs SET metrics=$3,metrics_at=now() WHERE id=$1 AND connection_epoch=$2`, id, epoch, msg.Payload)
			done()
			if e != nil {
				return nil
			}
			continue
		}
		if msg.Type != "heartbeat" {
			return nil
		}
		if !lastHeartbeat.IsZero() && time.Since(lastHeartbeat) < time.Second {
			return nil
		}
		lastHeartbeat = time.Now()
		c, done := context.WithTimeout(ctx, time.Second)
		tag, e := s.db.Exec(c, `UPDATE agent_gateway_configs SET last_heartbeat_at=now() WHERE id=$1 AND connection_epoch=$2 AND revoked_at IS NULL`, id, epoch)
		done()
		if e != nil || tag.RowsAffected() != 1 {
			return nil
		}
		var rotate bool
		if e = s.db.Row(ctx, `SELECT rotation_requested FROM agent_gateway_configs WHERE id=$1`, id).Scan(&rotate); e != nil {
			return nil
		}
		var update string
		if e = s.db.Row(ctx, `WITH prior AS (SELECT id,update_request FROM agent_gateway_configs WHERE id=$1 FOR UPDATE),changed AS (UPDATE agent_gateway_configs SET update_request='' WHERE id=$1) SELECT update_request FROM prior`, id).Scan(&update); e != nil {
			return nil
		}
		payload, _ := json.Marshal(map[string]any{"rotate": rotate, "update": update})
		if e = ch.send(agentwire.Control{V: 1, Type: "heartbeat_ack", ID: msg.ID, Epoch: epoch, Payload: payload}); e != nil {
			return nil
		}
	}
}
