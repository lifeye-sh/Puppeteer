//go:build linux

package rdpworker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
)

const maxControlFrame = 64 * 1024

var ErrControlProtocol = errors.New("invalid worker control protocol")

type ControlRequest struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Ticket string          `json:"ticket,omitempty"`
	// Payload carries ReportDeviceIO batches. Strict decoding treats any
	// other method arriving with a payload as a protocol violation.
	Payload json.RawMessage `json:"payload,omitempty"`
}
type ControlReply struct {
	ID         uint64          `json:"id"`
	SessionID  string          `json:"sessionId,omitempty"`
	LeaseUntil time.Time       `json:"leaseUntil,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

// ControlBackend must implement atomic ticket consumption, current resource
// authorization, audit, credential leases and finalization. The transport never
// chooses a target from worker input. A successful transport test is not evidence
// that those platform operations have been implemented.
type ControlBackend interface {
	ConsumeTicket(ctx context.Context, ticket string) (ControlReply, error)
	PrepareTarget(ctx context.Context, session string) (json.RawMessage, error)
	RenewSession(ctx context.Context, session string) (time.Time, error)
	Finalize(ctx context.Context, session string) error
	// ReportDeviceIO receives bounded worker-side client-drive operation
	// batches (already policy-filtered). Delivery is best effort: an error
	// only drops the audit payload, never the session.
	ReportDeviceIO(ctx context.Context, session string, payload json.RawMessage) error
}

func controlRead(conn net.Conn, out any) error {
	var header [4]byte
	if _, e := io.ReadFull(conn, header[:]); e != nil {
		return e
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > maxControlFrame {
		return ErrControlProtocol
	}
	data := make([]byte, n)
	defer clear(data)
	if _, e := io.ReadFull(conn, data); e != nil {
		return e
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return ErrControlProtocol
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return ErrControlProtocol
	}
	return nil
}
func controlWrite(conn net.Conn, value any) error {
	data, e := json.Marshal(value)
	defer clear(data)
	if e != nil || len(data) > maxControlFrame {
		return ErrControlProtocol
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	for _, b := range [][]byte{header[:], data} {
		for len(b) > 0 {
			n, e := conn.Write(b)
			if e != nil {
				return e
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			b = b[n:]
		}
	}
	return nil
}

// ServeControl owns conn. The first 32 bytes are the pipe-delivered nonce;
// subsequent messages are length-prefixed strict JSON. Any error closes the
// connection without returning potentially secret backend diagnostics.
func (s *Supervisor) ServeControl(ctx context.Context, conn *net.UnixConn, backend ControlBackend) error {
	return s.ServeControlFactory(ctx, conn, func(WorkerIdentity) ControlBackend { return backend })
}

// The factory runs only after bootstrap authentication, with the accepted
// client's IP and the worker's kernel identity bound by Run.
func (s *Supervisor) ServeControlFactory(ctx context.Context, conn *net.UnixConn, factory func(WorkerIdentity) ControlBackend) error {
	if conn == nil {
		return ErrControlProof
	}
	defer conn.Close()
	defer s.control.release(conn)
	if factory == nil {
		return ErrControlProtocol
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	nonce := make([]byte, 32)
	defer clear(nonce)
	if _, e := io.ReadFull(conn, nonce); e != nil {
		return ErrControlProof
	}
	if e := s.AuthorizeControl(conn, nonce); e != nil {
		return e
	}
	identity, ok := s.control.identity(conn)
	if !ok {
		return ErrControlProof
	}
	backend := factory(identity)
	if backend == nil {
		return ErrControlProtocol
	}
	return serveAuthorized(ctx, conn, backend)
}

func serveAuthorized(ctx context.Context, conn net.Conn, backend ControlBackend) error {
	session := ""
	var lease time.Time
	var previous uint64
	prepared := false
	// All paths after ticket consumption attempt cleanup. Finalize must be
	// idempotent; the platform remains responsible for recovery on crash.
	defer func() {
		if session != "" {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = backend.Finalize(cleanup, session)
		}
	}()
	for {
		deadline := time.Now().Add(5 * time.Second)
		// TLS client negotiation/user confirmation precedes ClientInfo (NTICKET).
		// Allow the bounded authentication window without lengthening backend calls.
		if session == "" {
			deadline = time.Now().Add(30 * time.Second)
		}
		if session != "" {
			if !time.Now().Before(lease) {
				return ErrControlProtocol
			}
			if lease.Before(deadline) {
				deadline = lease
			}
		}
		conn.SetDeadline(deadline)
		var req ControlRequest
		if e := controlRead(conn, &req); e != nil {
			return e
		}
		if req.ID == 0 || req.ID <= previous {
			return ErrControlProtocol
		}
		previous = req.ID
		if session != "" && !time.Now().Before(lease) {
			return ErrControlProtocol
		}
		callDeadline := time.Now().Add(5 * time.Second)
		if deadline.Before(callDeadline) {
			callDeadline = deadline
		}
		call, cancel := context.WithDeadline(ctx, callDeadline)
		reply := ControlReply{ID: req.ID}
		var err error
		finish := false
		switch req.Method {
		case "ConsumeTicket":
			if session != "" || len(req.Ticket) < 1 || len(req.Ticket) > 1024 {
				cancel()
				return ErrControlProtocol
			}
			reply, err = backend.ConsumeTicket(call, req.Ticket)
			reply.ID = req.ID
			if err == nil {
				session = reply.SessionID
				lease = reply.LeaseUntil
				if session == "" || len(session) > 128 || !validLease(lease) {
					err = ErrControlProtocol
				}
			}
		case "PrepareTarget":
			if session == "" || prepared || req.Ticket != "" {
				cancel()
				return ErrControlProtocol
			}
			prepared = true
			reply.Payload, err = backend.PrepareTarget(call, session)
		case "RenewSession":
			if session == "" || req.Ticket != "" {
				cancel()
				return ErrControlProtocol
			}
			lease, err = backend.RenewSession(call, session)
			reply.LeaseUntil = lease
			if err == nil && !validLease(lease) {
				err = ErrControlProtocol
			}
		case "ReportDeviceIO":
			if session == "" || req.Ticket != "" || len(req.Payload) == 0 ||
				len(req.Payload) > maxControlFrame {
				cancel()
				return ErrControlProtocol
			}
			// Best effort: a rejected audit batch never tears down the session.
			_ = backend.ReportDeviceIO(call, session, req.Payload)
		case "Finalize":
			if session == "" || req.Ticket != "" {
				cancel()
				return ErrControlProtocol
			}
			err = backend.Finalize(call, session)
			if err == nil {
				session = ""
			}
			finish = true
		default:
			err = ErrControlProtocol
		}
		if err == nil {
			err = call.Err()
		}
		cancel()
		if err != nil {
			clear(reply.Payload)
			return ErrControlProtocol
		}
		e := controlWrite(conn, reply)
		clear(reply.Payload)
		if e != nil {
			return e
		}
		if finish {
			return nil
		}
	}
}
func validLease(deadline time.Time) bool {
	now := time.Now()
	return deadline.After(now) && !deadline.After(now.Add(15*time.Second))
}
