package pgproxy

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/xdg-go/scram"
	"next-terminal/backend/internal/modules/databaseauth"
)

type Parameter struct {
	Type   uint32 `json:"type"`
	Bytes  int    `json:"bytes"`
	Null   bool   `json:"null"`
	Format int16  `json:"format"`
}
type Session struct {
	MaxRows  uint64
	MaxBytes int
	Target   *pgx.Conn
	Check    func(context.Context) error
	Start    func(context.Context, string, byte, []Parameter) (string, error)
	Finish   func(context.Context, string, bool, uint64, time.Duration) error
	Close    func()
}
type Lookup func(context.Context, string, string, string) (databaseauth.SCRAMMaterial, func(context.Context) (*Session, error), error)
type cancelEntry struct {
	key     []byte
	address string
	cancel  func(context.Context) error
}
type Server struct {
	TLS           *tls.Config
	Lookup        Lookup
	Failed        func(context.Context, string)
	mu            sync.Mutex
	cancellations map[uint32]cancelEntry
}

func send(b *pgproto3.Backend, m pgproto3.BackendMessage) error { b.Send(m); return b.Flush() }
func protocolError(b *pgproto3.Backend, code, message string) error {
	return send(b, &pgproto3.ErrorResponse{Severity: "ERROR", Code: code, Message: message})
}
func (s *Server) ServeConn(parent context.Context, raw net.Conn) {
	defer raw.Close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	_ = raw.SetDeadline(time.Now().Add(15 * time.Second))
	backend := pgproto3.NewBackend(raw, raw)
	msg, e := backend.ReceiveStartupMessage()
	if e != nil {
		return
	}
	if request, ok := msg.(*pgproto3.CancelRequest); ok {
		s.cancel(ctx, raw.RemoteAddr(), request)
		return
	}
	if _, ok := msg.(*pgproto3.GSSEncRequest); ok {
		if _, e = raw.Write([]byte{'N'}); e != nil {
			return
		}
		msg, e = backend.ReceiveStartupMessage()
		if e != nil {
			return
		}
	}
	if _, ok := msg.(*pgproto3.SSLRequest); !ok {
		_ = protocolError(backend, "28000", "TLS is required")
		return
	}
	if _, e = raw.Write([]byte{'S'}); e != nil {
		return
	}
	secure := tls.Server(raw, s.TLS)
	if e = secure.HandshakeContext(ctx); e != nil {
		return
	}
	backend = pgproto3.NewBackend(secure, secure)
	backend.SetMaxBodyLen(1 << 20)
	msg, e = backend.ReceiveStartupMessage()
	if e != nil {
		return
	}
	if request, ok := msg.(*pgproto3.CancelRequest); ok {
		s.cancel(ctx, raw.RemoteAddr(), request)
		return
	}
	startup, ok := msg.(*pgproto3.StartupMessage)
	if !ok {
		return
	}
	if startup.ProtocolVersion != pgproto3.ProtocolVersion30 && startup.ProtocolVersion != pgproto3.ProtocolVersion32 {
		return
	}
	if startup.ProtocolVersion == pgproto3.ProtocolVersion32 {
		if e = send(backend, &pgproto3.NegotiateProtocolVersion{NewestMinorProtocol: 0}); e != nil {
			return
		}
	}
	deny := func() {
		_ = protocolError(backend, "28P01", "Database authentication or authorization failed")
		if s.Failed != nil {
			s.Failed(ctx, raw.RemoteAddr().String())
		}
	}
	// No startup options, role, search_path or replication setting can bypass policy.
	for k, v := range startup.Parameters {
		switch k {
		case "user", "database":
		case "application_name", "TimeZone":
			if len(v) > 256 || v == "" {
				deny()
				return
			}
		case "DateStyle":
			if v != "ISO" {
				deny()
				return
			}
		case "extra_float_digits":
			// JDBC sends this bounded formatting preference during startup.
			n, err := strconv.Atoi(v)
			if err != nil || n < -15 || n > 3 {
				deny()
				return
			}
		case "client_encoding":
			if v != "UTF8" && v != "UTF-8" {
				deny()
				return
			}
		default:
			deny()
			return
		}
	}
	material, open, e := s.Lookup(ctx, startup.Parameters["user"], startup.Parameters["database"], raw.RemoteAddr().String())
	if e != nil {
		deny()
		return
	}
	defer clear(material.StoredKey)
	defer clear(material.ServerKey)
	auth, e := scram.SHA256.NewServer(func(string) (scram.StoredCredentials, error) {
		return scram.StoredCredentials{KeyFactors: scram.KeyFactors{Salt: string(material.Salt), Iters: material.Iterations}, StoredKey: material.StoredKey, ServerKey: material.ServerKey}, nil
	})
	if e != nil {
		deny()
		return
	}
	conversation := auth.NewConversation()
	if e = backend.SetAuthType(pgproto3.AuthTypeSASL); e != nil {
		return
	}
	if e = send(backend, &pgproto3.AuthenticationSASL{AuthMechanisms: []string{"SCRAM-SHA-256"}}); e != nil {
		return
	}
	first, e := backend.Receive()
	if e != nil {
		return
	}
	initial, ok := first.(*pgproto3.SASLInitialResponse)
	if !ok || initial.AuthMechanism != "SCRAM-SHA-256" || len(initial.Data) > 4096 {
		deny()
		return
	}
	challenge, e := conversation.Step(string(initial.Data))
	if e != nil {
		deny()
		return
	}
	if e = backend.SetAuthType(pgproto3.AuthTypeSASLContinue); e != nil {
		return
	}
	if e = send(backend, &pgproto3.AuthenticationSASLContinue{Data: []byte(challenge)}); e != nil {
		return
	}
	last, e := backend.Receive()
	if e != nil {
		return
	}
	response, ok := last.(*pgproto3.SASLResponse)
	if !ok || len(response.Data) > 4096 {
		deny()
		return
	}
	final, e := conversation.Step(string(response.Data))
	if e != nil || !conversation.Done() || !conversation.Valid() || conversation.AuthzID() != "" {
		deny()
		return
	}
	session, e := open(ctx)
	if e != nil {
		deny()
		return
	}
	defer session.Close()
	// Connection.Close() sends protocol messages; only close the underlying socket
	// concurrently. All upstream protocol reads/writes belong to Relay.
	stopTarget := context.AfterFunc(ctx, func() { session.Target.PgConn().Conn().Close() })
	defer stopTarget()
	defer session.Target.Close(context.Background())
	// Apply only approved formatting preferences using protocol parameters. Never
	// interpolate startup values into SQL or forward options/search_path/role.
	setupCtx, finishSetup := context.WithTimeout(ctx, 5*time.Second)
	for _, name := range []string{"application_name", "TimeZone", "DateStyle", "extra_float_digits"} {
		if value, ok := startup.Parameters[name]; ok {
			var applied string
			if e = session.Target.QueryRow(setupCtx, "SELECT pg_catalog.set_config($1,$2,false)", pgx.QueryExecModeExec, name, value).Scan(&applied); e != nil {
				finishSetup()
				deny()
				return
			}
		}
	}
	finishSetup()
	if e = send(backend, &pgproto3.AuthenticationSASLFinal{Data: []byte(final)}); e != nil {
		return
	}
	if e = backend.SetAuthType(pgproto3.AuthTypeOk); e != nil {
		return
	}
	backend.Send(&pgproto3.AuthenticationOk{})
	for _, name := range []string{"server_version", "server_encoding", "client_encoding", "DateStyle", "TimeZone", "integer_datetimes", "standard_conforming_strings"} {
		if value := session.Target.PgConn().ParameterStatus(name); value != "" {
			backend.Send(&pgproto3.ParameterStatus{Name: name, Value: value})
		}
	}
	backend.Send(&pgproto3.ParameterStatus{Name: "session_authorization", Value: startup.Parameters["user"]})
	backend.Send(&pgproto3.ParameterStatus{Name: "default_transaction_read_only", Value: "on"})
	pid, key, release, e := s.register(raw.RemoteAddr(), session.Target.PgConn().CancelRequest)
	if e != nil {
		return
	}
	defer release()
	backend.Send(&pgproto3.BackendKeyData{ProcessID: pid, SecretKey: key})
	if e = send(backend, &pgproto3.ReadyForQuery{TxStatus: 'I'}); e != nil {
		return
	}
	_ = raw.SetDeadline(time.Time{})
	// Authorization lease closes idle as well as busy connections.
	done := make(chan struct{})
	defer func() { cancel(); <-done }()
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c, finish := context.WithTimeout(ctx, 3*time.Second)
				err := session.Check(c)
				finish()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	relay := Relay{Downstream: backend, Upstream: session.Target.PgConn().Frontend(), Client: secure, Target: session.Target.PgConn().Conn(), Session: session}
	_ = relay.Serve(ctx)
}
func remoteHost(a net.Addr) string {
	h, _, e := net.SplitHostPort(a.String())
	if e != nil {
		return a.String()
	}
	return h
}
func (s *Server) register(addr net.Addr, fn func(context.Context) error) (uint32, []byte, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancellations == nil {
		s.cancellations = map[uint32]cancelEntry{}
	}
	for i := 0; i < 10; i++ {
		var b [8]byte
		if _, e := rand.Read(b[:]); e != nil {
			return 0, nil, nil, e
		}
		pid := binary.BigEndian.Uint32(b[:4])
		key := append([]byte(nil), b[4:]...)
		if _, ok := s.cancellations[pid]; ok || pid == 0 {
			continue
		}
		s.cancellations[pid] = cancelEntry{key, remoteHost(addr), fn}
		return pid, key, func() { s.mu.Lock(); delete(s.cancellations, pid); s.mu.Unlock() }, nil
	}
	return 0, nil, nil, errors.New("cancellation token allocation failed")
}
func (s *Server) cancel(ctx context.Context, addr net.Addr, r *pgproto3.CancelRequest) {
	s.mu.Lock()
	v, ok := s.cancellations[r.ProcessID]
	s.mu.Unlock()
	if !ok || v.address != remoteHost(addr) || subtle.ConstantTimeCompare(v.key, r.SecretKey) != 1 {
		return
	}
	c, done := context.WithTimeout(ctx, 3*time.Second)
	defer done()
	_ = v.cancel(c)
}
