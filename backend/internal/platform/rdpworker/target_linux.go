//go:build linux

package rdpworker

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// TargetBridge accepts exactly one authenticated worker connection. It does not
// dial until SO_PEERCRED matches; the dial callback must recheck platform policy.
// The independent lease timer closes both sockets even if control RPC stalls.
type TargetBridge struct {
	mu              sync.Mutex
	path, dir       string
	listener        *net.UnixListener
	local, upstream net.Conn
	timer           *time.Timer
	expiry          time.Time
	closed          bool
	cancel          context.CancelFunc
	done            chan struct{}
}

func NewTargetBridge(parent context.Context, root string, peer WorkerIdentity, expiry time.Time, dial func(context.Context) (net.Conn, error)) (*TargetBridge, error) {
	if dial == nil || !validLease(expiry) {
		return nil, errors.New("invalid target bridge configuration")
	}
	listener, dir, path, e := openWorkerSocket(root, peer, "target.sock")
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(parent)
	b := &TargetBridge{path: path, dir: dir, listener: listener, expiry: expiry, cancel: cancel, done: make(chan struct{})}
	b.mu.Lock()
	b.timer = time.AfterFunc(time.Until(expiry), b.expire)
	b.mu.Unlock()
	go func() {
		defer close(b.done)
		stop := context.AfterFunc(ctx, b.Close)
		defer stop()
		defer b.Close()
		b.serve(ctx, peer, dial)
	}()
	return b, nil
}

func openWorkerSocket(root string, peer WorkerIdentity, name string) (*net.UnixListener, string, string, error) {
	info, e := os.Lstat(root)
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 || !filepath.IsAbs(root) || peer.PID <= 0 {
		return nil, "", "", errors.New("invalid private worker socket configuration")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return nil, "", "", ErrControlProof
	}
	dir, e := os.MkdirTemp(root, "rdp-")
	if e != nil {
		return nil, "", "", e
	}
	path := filepath.Join(dir, name)
	if len(path) > 100 {
		os.Remove(dir)
		return nil, "", "", errors.New("worker socket path too long")
	}
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		os.Remove(dir)
		return nil, "", "", e
	}
	if e = os.Chmod(path, 0600); e != nil {
		listener.Close()
		os.Remove(dir)
		return nil, "", "", e
	}
	return listener, dir, path, nil
}
func (b *TargetBridge) Path() string          { return b.path }
func (b *TargetBridge) Done() <-chan struct{} { return b.done }
func (b *TargetBridge) Renew(expiry time.Time) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || !time.Now().Before(b.expiry) || !validLease(expiry) {
		return ErrControlProof
	}
	b.expiry = expiry
	b.timer.Reset(time.Until(expiry))
	return nil
}
func (b *TargetBridge) expire() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	if time.Now().Before(b.expiry) {
		b.timer.Reset(time.Until(b.expiry))
		return
	}
	b.closeLocked()
}
func (b *TargetBridge) Close() { b.mu.Lock(); defer b.mu.Unlock(); b.closeLocked() }
func (b *TargetBridge) closeLocked() {
	if b.closed {
		return
	}
	b.closed = true
	b.cancel()
	b.timer.Stop()
	b.listener.Close()
	if b.local != nil {
		b.local.Close()
	}
	if b.upstream != nil {
		b.upstream.Close()
	}
	os.Remove(b.path)
	os.Remove(b.dir)
}
func acceptWorker(listener *net.UnixListener, peer WorkerIdentity) (*net.UnixConn, error) {
	for {
		conn, e := listener.AcceptUnix()
		if e != nil {
			return nil, e
		}
		raw, e := conn.SyscallConn()
		var cred *syscall.Ucred
		var se error
		if e == nil {
			e = raw.Control(func(fd uintptr) { cred, se = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED) })
		}
		if e != nil || se != nil || cred == nil || cred.Pid != peer.PID || cred.Uid != peer.UID {
			conn.Close()
			continue
		}
		return conn, nil
	}
}

func (b *TargetBridge) serve(ctx context.Context, peer WorkerIdentity, dial func(context.Context) (net.Conn, error)) {
	conn, e := acceptWorker(b.listener, peer)
	if e != nil {
		return
	}
	b.listener.Close()
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		conn.Close()
		return
	}
	b.local = conn
	b.mu.Unlock()
	dc, cancel := context.WithTimeout(ctx, 5*time.Second)
	upstream, e := dial(dc)
	cancel()
	if e != nil {
		if upstream != nil {
			upstream.Close()
		}
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		upstream.Close()
		return
	}
	b.upstream = upstream
	b.mu.Unlock()
	done := make(chan struct{})
	go func() { io.Copy(upstream, conn); b.Close(); close(done) }()
	io.Copy(conn, upstream)
	b.Close()
	<-done
}
