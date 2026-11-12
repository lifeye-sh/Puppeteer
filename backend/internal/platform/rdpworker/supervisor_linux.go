//go:build linux

// Package rdpworker supervises one native RDP worker per accepted connection.
// This process boundary does not itself authenticate RDP or authorize a target.
package rdpworker

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

var ErrCapacity = errors.New("native RDP worker capacity reached")

type Supervisor struct {
	path              string
	args              []string
	slots             chan struct{}
	startup, shutdown time.Duration
	control           controlGate
}

func New(path string, args []string, capacity int, startup, shutdown time.Duration) (*Supervisor, error) {
	if !filepath.IsAbs(path) || capacity < 1 || capacity > 256 || startup <= 0 || startup > 30*time.Second || shutdown <= 0 || shutdown > 5*time.Second {
		return nil, errors.New("invalid worker configuration")
	}
	return &Supervisor{path: path, args: append([]string(nil), args...), slots: make(chan struct{}, capacity), startup: startup, shutdown: shutdown}, nil
}

// Run owns and closes conn, including on admission failure. FD 3 is the accepted
// socket, FD 4 a private bootstrap pipe, and FD 5 the readiness pipe (exactly
// "READY\n"). The bootstrap contains only a fresh 32-byte nonce generated
// here, for one authenticated control connection; never target credentials.
// Secrets never enter argv, environment, or captured worker output.
func (s *Supervisor) Run(ctx context.Context, conn *net.TCPConn) error {
	if conn == nil {
		return errors.New("missing accepted socket")
	}
	defer conn.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	bootstrap := make([]byte, 32)
	if _, err := rand.Read(bootstrap); err != nil {
		return errors.New("worker nonce generation failed")
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return ErrCapacity
	}
	socket, err := conn.File()
	if err != nil {
		return err
	}
	defer socket.Close()
	initRead, initWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer initRead.Close()
	defer initWrite.Close()
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer readyRead.Close()
	defer readyWrite.Close()
	cmd := exec.Command(s.path, s.args...)
	cmd.Env = []string{"LANG=C.UTF-8"}
	cmd.ExtraFiles = []*os.File{socket, initRead, readyWrite}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	// Linux delivers Pdeathsig when the creating thread exits, not just when
	// the process exits. Keep that thread alive until this worker is reaped.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("start RDP worker: %w", err)
	}
	s.control.register(int32(cmd.Process.Pid), uint32(os.Geteuid()), bootstrap, time.Now().Add(s.startup), conn.RemoteAddr().(*net.TCPAddr).IP.String())
	defer s.control.revoke(int32(cmd.Process.Pid))
	defer syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	// Parent must not retain a writer that masks worker readiness EOF.
	initRead.Close()
	readyWrite.Close()
	socket.Close()
	exited := make(chan error, 1)
	waitResult := exited
	go func() { waitResult <- cmd.Wait() }()
	exitSeen := false
	var exitErr error
	stop := func() {
		if exitSeen {
			return
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		timer := time.NewTimer(s.shutdown)
		defer timer.Stop()
		select {
		case <-exited:
		case <-timer.C:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-exited
		}
	}
	initResult := make(chan error, 1)
	go func() { _, e := initWrite.Write(bootstrap); initWrite.Close(); initResult <- e }()
	ready := make(chan error, 1)
	go func() {
		b := make([]byte, 6)
		_, e := io.ReadFull(readyRead, b)
		if e == nil && string(b) != "READY\n" {
			e = errors.New("invalid readiness message")
		}
		ready <- e
	}()
	timer := time.NewTimer(s.startup)
	defer timer.Stop()
	initialized, started := false, false
	for !initialized || !started {
		select {
		case err = <-initResult:
			if err != nil {
				stop()
				return errors.New("worker bootstrap failed")
			}
			initialized = true
			initResult = nil
		case err = <-ready:
			if err != nil {
				stop()
				return errors.New("worker readiness failed")
			}
			started = true
			ready = nil
		case exitErr = <-exited:
			exitSeen = true
			exited = nil
		case <-timer.C:
			stop()
			return errors.New("worker startup timed out")
		case <-ctx.Done():
			stop()
			return ctx.Err()
		}
	}
	if exitSeen {
		if exitErr != nil {
			return fmt.Errorf("RDP worker failed: %w", exitErr)
		}
		return nil
	}
	select {
	case err = <-exited:
		// Reap any children left behind by a worker that exited unexpectedly.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err != nil {
			return fmt.Errorf("RDP worker failed: %w", err)
		}
		return nil
	case <-ctx.Done():
		stop()
		return ctx.Err()
	}
}
