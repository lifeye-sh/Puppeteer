package pgproxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

type statement struct {
	sql   string
	types []uint32
}
type portal struct {
	sql        string
	parameters []Parameter
}
type Relay struct {
	Downstream     *pgproto3.Backend
	Upstream       *pgproto3.Frontend
	Client, Target net.Conn
	Session        *Session
	failed         bool
	tx             byte
	statements     map[string]statement
	portals        map[string]portal
	describing     *string
}
type received struct {
	m pgproto3.FrontendMessage
	e error
}

func (r *Relay) Serve(ctx context.Context) error {
	if r.Session.MaxRows == 0 {
		r.Session.MaxRows = 100000
	}
	if r.Session.MaxBytes == 0 {
		r.Session.MaxBytes = 64 << 20
	}
	r.statements = map[string]statement{}
	r.portals = map[string]portal{}
	r.tx = 'I'
	r.Upstream.SetMaxBodyLen(8 << 20)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	input := make(chan received, 1)
	done := make(chan struct{})
	// A one-message queue bounds pipelining memory and detects idle disconnects.
	go func() {
		defer close(done)
		for {
			m, e := r.Downstream.Receive()
			if e != nil {
				cancel()
				r.Target.Close()
				select {
				case input <- received{e: e}:
				case <-ctx.Done():
				}
				return
			}
			raw, e := m.Encode(nil)
			if e != nil {
				cancel()
				r.Target.Close()
				return
			}
			copyBackend := pgproto3.NewBackend(bytes.NewReader(raw), io.Discard)
			copyBackend.SetMaxBodyLen(1 << 20)
			copyMessage, e := copyBackend.Receive()
			select {
			case input <- received{copyMessage, e}:
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); r.Client.Close(); <-done }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-input:
			if event.e != nil {
				return event.e
			}
			if _, ok := event.m.(*pgproto3.Terminate); ok {
				return nil
			}
			if r.failed {
				if _, ok := event.m.(*pgproto3.Sync); !ok {
					continue
				}
			}
			check, finish := context.WithTimeout(ctx, 3*time.Second)
			e := r.Session.Check(check)
			finish()
			if e != nil {
				return e
			}
			if e = r.handle(ctx, event.m); e != nil {
				return e
			}
		}
	}
}
func (r *Relay) forward(m pgproto3.FrontendMessage, flush bool) error {
	_ = r.Target.SetDeadline(time.Now().Add(30 * time.Second))
	r.Upstream.Send(m)
	if flush {
		r.Upstream.Send(&pgproto3.Flush{})
	}
	return r.Upstream.Flush()
}
func (r *Relay) output(m pgproto3.BackendMessage) error {
	_ = r.Client.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return send(r.Downstream, m)
}

// Replies are drained after each message (with an injected Flush), so pipelined
// clients retain order without sharing a target connection across users.
func (r *Relay) drain(until string) (bool, uint64, error) {
	failed := false
	var rows uint64
	var total int
	for {
		m, e := r.Upstream.Receive()
		if e != nil {
			return failed, rows, e
		}
		packet, e := m.Encode(nil)
		if e != nil {
			return failed, rows, e
		}
		total += len(packet)
		if total > r.Session.MaxBytes {
			return failed, rows, errors.New("PostgreSQL result limit exceeded")
		}
		switch v := m.(type) {
		case *pgproto3.CommandComplete:
			if tag := string(v.CommandTag); strings.HasPrefix(tag, "COPY ") {
				rows, _ = strconv.ParseUint(strings.TrimPrefix(tag, "COPY "), 10, 64)
			}
		case *pgproto3.ParameterDescription:
			if r.describing != nil {
				if statement, ok := r.statements[*r.describing]; ok {
					statement.types = append([]uint32(nil), v.ParameterOIDs...)
					r.statements[*r.describing] = statement
				}
			}
		case *pgproto3.ErrorResponse:
			failed = true
			r.failed = until != "query" && until != "sync"
		case *pgproto3.DataRow:
			rows++
			if rows > r.Session.MaxRows {
				return failed, rows, errors.New("PostgreSQL row limit exceeded")
			}
		case *pgproto3.ReadyForQuery:
			r.tx = v.TxStatus
			r.failed = false
			if v.TxStatus == 'I' {
				r.portals = map[string]portal{}
			}
		case *pgproto3.CopyInResponse, *pgproto3.CopyBothResponse:
			return true, rows, ErrSQLDenied
		}
		if e = r.output(m); e != nil {
			return failed, rows, e
		}
		switch m.(type) {
		case *pgproto3.ReadyForQuery:
			if until == "query" || until == "sync" {
				return failed, rows, nil
			}
		case *pgproto3.ErrorResponse:
			if until != "query" && until != "sync" {
				return true, rows, nil
			}
		case *pgproto3.ParseComplete:
			if until == "parse" {
				return false, rows, nil
			}
		case *pgproto3.BindComplete:
			if until == "bind" {
				return false, rows, nil
			}
		case *pgproto3.CloseComplete:
			if until == "close" {
				return false, rows, nil
			}
		case *pgproto3.RowDescription, *pgproto3.NoData:
			if until == "describe" {
				return false, rows, nil
			}
		case *pgproto3.CommandComplete, *pgproto3.PortalSuspended, *pgproto3.EmptyQueryResponse:
			if until == "execute" {
				return failed, rows, nil
			}
		}
	}
}

// Rejecting a command must also abort a target transaction and make the target
// ignore extended messages until Sync. A known syntax error achieves the same
// PostgreSQL error state without executing client SQL or an unapproved write.
func (r *Relay) reject(extended bool) error {
	var m pgproto3.FrontendMessage = &pgproto3.Query{String: "SELECT ("}
	if extended {
		m = &pgproto3.Parse{Query: "SELECT ("}
	}
	if e := r.forward(m, extended); e != nil {
		return e
	}
	for {
		reply, e := r.Upstream.Receive()
		if e != nil {
			return e
		}
		switch v := reply.(type) {
		case *pgproto3.ErrorResponse:
			if extended {
				r.failed = true
				return r.output(&pgproto3.ErrorResponse{Severity: "ERROR", Code: "42501", Message: "Command is not allowed by the database access policy"})
			}
		case *pgproto3.ReadyForQuery:
			r.tx = v.TxStatus
			if e := r.output(&pgproto3.ErrorResponse{Severity: "ERROR", Code: "42501", Message: "Command is not allowed by the database access policy"}); e != nil {
				return e
			}
			return r.output(v)
		}
	}
}
func (r *Relay) audited(ctx context.Context, sql string, command byte, parameters []Parameter, extended bool, run func() (bool, uint64, error)) error {
	start := time.Now()
	id, e := r.Session.Start(ctx, sql, command, parameters)
	if e != nil {
		return e
	}
	if e = ValidateSQL(sql); e != nil {
		if e = r.Session.Finish(ctx, id, true, 0, time.Since(start)); e != nil {
			return e
		}
		return r.reject(extended)
	}
	failed, rows, e := run()
	if e != nil {
		return e
	} // Disconnect leaves an explicit unknown via EndSession.
	return r.Session.Finish(ctx, id, failed, rows, time.Since(start))
}
func (r *Relay) handle(ctx context.Context, m pgproto3.FrontendMessage) error {
	switch v := m.(type) {
	case *pgproto3.Query:
		delete(r.statements, "")
		delete(r.portals, "")
		return r.audited(ctx, v.String, 'Q', nil, false, func() (bool, uint64, error) {
			if e := r.forward(v, false); e != nil {
				return true, 0, e
			}
			return r.drain("query")
		})
	case *pgproto3.Parse:
		if len(v.Name) > 256 || len(v.ParameterOIDs) > 1024 {
			return r.reject(true)
		}
		total := len(v.Query)
		for name, s := range r.statements {
			if name != v.Name {
				total += len(s.sql)
			}
		}
		if total > 4<<20 || len(r.statements) >= 256 && v.Name != "" {
			return r.reject(true)
		}
		return r.audited(ctx, v.Query, 'P', nil, true, func() (bool, uint64, error) {
			if e := r.forward(v, true); e != nil {
				return true, 0, e
			}
			bad, n, e := r.drain("parse")
			if e == nil && !bad {
				r.statements[v.Name] = statement{v.Query, append([]uint32(nil), v.ParameterOIDs...)}
			}
			return bad, n, e
		})
	case *pgproto3.Bind:
		s, ok := r.statements[v.PreparedStatement]
		if !ok || len(v.Parameters) > 1024 || len(v.DestinationPortal) > 256 || len(r.portals) >= 256 && v.DestinationPortal != "" {
			return r.reject(true)
		}
		parameters := make([]Parameter, len(v.Parameters))
		for i, p := range v.Parameters {
			parameters[i] = Parameter{Bytes: len(p), Null: p == nil}
			if i < len(s.types) {
				parameters[i].Type = s.types[i]
			}
			if len(v.ParameterFormatCodes) == 1 {
				parameters[i].Format = v.ParameterFormatCodes[0]
			} else if i < len(v.ParameterFormatCodes) {
				parameters[i].Format = v.ParameterFormatCodes[i]
			}
		}
		if e := r.forward(v, true); e != nil {
			return e
		}
		bad, _, e := r.drain("bind")
		if e == nil && !bad {
			r.portals[v.DestinationPortal] = portal{s.sql, parameters}
		}
		return e
	case *pgproto3.Describe:
		if v.ObjectType == 'S' {
			r.describing = &v.Name
		}
		defer func() { r.describing = nil }()
		if e := r.forward(v, true); e != nil {
			return e
		}
		_, _, e := r.drain("describe")
		return e
	case *pgproto3.Execute:
		p, ok := r.portals[v.Portal]
		if !ok {
			return r.reject(true)
		}
		return r.audited(ctx, p.sql, 'E', p.parameters, true, func() (bool, uint64, error) {
			if e := r.forward(v, true); e != nil {
				return true, 0, e
			}
			return r.drain("execute")
		})
	case *pgproto3.Close:
		if e := r.forward(v, true); e != nil {
			return e
		}
		bad, _, e := r.drain("close")
		if e == nil && !bad {
			if v.ObjectType == 'S' {
				delete(r.statements, v.Name)
			} else if v.ObjectType == 'P' {
				delete(r.portals, v.Name)
			}
		}
		return e
	case *pgproto3.Sync:
		if e := r.forward(v, false); e != nil {
			return e
		}
		_, _, e := r.drain("sync")
		return e
	case *pgproto3.Flush:
		return r.Downstream.Flush()
	default:
		return errors.New("unsupported PostgreSQL frontend message")
	}
}
