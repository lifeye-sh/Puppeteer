//go:build linux

package rdpworker

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"
)

var ErrControlProof = errors.New("invalid worker control proof")

type controlBinding struct {
	uid      uint32
	digest   [32]byte
	expires  time.Time
	sourceIP string
}

// Identity is established by the supervisor and the kernel, never RPC input.
type WorkerIdentity struct {
	ID       string
	PID      int32
	UID      uint32
	SourceIP string
}

type controlGate struct {
	mu          sync.Mutex
	bindings    map[int32]controlBinding
	connections map[int32]*net.UnixConn
	identities  map[*net.UnixConn]WorkerIdentity
}

func (g *controlGate) register(pid int32, uid uint32, nonce []byte, expires time.Time, source ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.bindings == nil {
		g.bindings = make(map[int32]controlBinding)
	}
	b := controlBinding{uid: uid, digest: sha256.Sum256(nonce), expires: expires}
	if len(source) == 1 {
		b.sourceIP = source[0]
	}
	g.bindings[pid] = b
}
func (g *controlGate) revoke(pid int32) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.bindings, pid)
	if conn := g.connections[pid]; conn != nil {
		delete(g.identities, conn)
		conn.Close()
		delete(g.connections, pid)
	}
}

// AuthorizeControl consumes a worker's bootstrap proof on an accepted Unix
// connection. Identity comes from SO_PEERCRED, never request JSON. The caller
// must close rejected connections and retain an accepted connection as the
// worker's private control channel. This is not asset authorization or a lease.
func (s *Supervisor) AuthorizeControl(conn *net.UnixConn, nonce []byte) error {
	return s.control.consume(conn, nonce)
}
func (g *controlGate) consume(conn *net.UnixConn, nonce []byte) error {
	if conn == nil || len(nonce) != 32 {
		return ErrControlProof
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return ErrControlProof
	}
	var peer *syscall.Ucred
	var socketErr error
	if err = raw.Control(func(fd uintptr) {
		peer, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil || socketErr != nil || peer == nil {
		return ErrControlProof
	}
	digest := sha256.Sum256(nonce)
	g.mu.Lock()
	defer g.mu.Unlock()
	binding, ok := g.bindings[peer.Pid]
	if !ok {
		return ErrControlProof
	}
	if !time.Now().Before(binding.expires) {
		delete(g.bindings, peer.Pid)
		return ErrControlProof
	}
	if peer.Uid != binding.uid || subtle.ConstantTimeCompare(digest[:], binding.digest[:]) != 1 {
		return ErrControlProof
	}
	delete(g.bindings, peer.Pid)
	if g.connections == nil {
		g.connections = make(map[int32]*net.UnixConn)
	}
	g.connections[peer.Pid] = conn
	if g.identities == nil {
		g.identities = make(map[*net.UnixConn]WorkerIdentity)
	}
	b := digest[:16]
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	g.identities[conn] = WorkerIdentity{ID: fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), PID: peer.Pid, UID: peer.Uid, SourceIP: binding.sourceIP}
	return nil
}

func (g *controlGate) identity(conn *net.UnixConn) (WorkerIdentity, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	v, ok := g.identities[conn]
	return v, ok
}

func (g *controlGate) release(conn *net.UnixConn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.identities, conn)
	for pid, c := range g.connections {
		if c == conn {
			delete(g.connections, pid)
		}
	}
}
