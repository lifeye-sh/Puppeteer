//go:build linux

package application

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"next-terminal/backend/internal/platform/rdprecording"
	"next-terminal/backend/internal/platform/rdpworker"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StartNativeRDP starts a loopback-only recorded desktop listener. Every
// accepted connection gets a new supervisor, private control socket and HOME.
func (a *App) StartNativeRDP(ctx context.Context, addr, worker, cert, key, ffmpeg string) (string, error) {
	return a.startRecordedListener(ctx, addr, "", worker, cert, key, ffmpeg)
}

// StartContainerRDP is for the local Compose profile. Docker maps the TCP
// listener to 127.0.0.1 on the host. The advertised name matches the TLS SAN.
func (a *App) StartContainerRDP(ctx context.Context, worker, cert, key, ffmpeg string) (string, error) {
	return a.startRecordedListener(ctx, "0.0.0.0:13389", "backend.localhost:13389", worker, cert, key, ffmpeg)
}

func (a *App) startRecordedListener(ctx context.Context, addr, advertised, worker, cert, key, ffmpeg string) (string, error) {
	target, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil || target.IP == nil || (advertised == "" && !target.IP.IsLoopback()) {
		return "", errors.New("native RDP requires a loopback listener")
	}
	for _, path := range []string{worker, ffmpeg, cert, key} {
		if !filepath.IsAbs(path) {
			return "", errors.New("absolute native RDP paths required")
		}
	}
	for _, path := range []string{worker, ffmpeg} {
		st, e := os.Stat(path)
		if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
			return "", errors.New("native RDP executable unavailable")
		}
	}
	if _, err = tls.LoadX509KeyPair(cert, key); err != nil {
		return "", err
	}
	listener, err := net.ListenTCP("tcp", target)
	if err != nil {
		return "", err
	}
	endpoint := listener.Addr().String()
	if advertised != "" {
		endpoint = advertised
	}
	if !a.nativeRDPAddress.CompareAndSwap(nil, &endpoint) {
		listener.Close()
		return "", errors.New("native RDP already running")
	}
	go func() {
		defer a.nativeRDPAddress.CompareAndSwap(&endpoint, nil)
		defer listener.Close()
		stop := context.AfterFunc(ctx, func() { listener.Close() })
		defer stop()
		slots := make(chan struct{}, 2)
		for {
			conn, e := listener.AcceptTCP()
			if e != nil {
				return
			}
			select {
			case slots <- struct{}{}:
			default:
				conn.Close()
				continue
			}
			go func() { defer func() { <-slots }(); a.serveRecordedPeer(ctx, conn, worker, cert, key, ffmpeg) }()
		}
	}()
	return endpoint, nil
}

func (a *App) serveRecordedPeer(parent context.Context, conn *net.TCPConn, worker, cert, key, ffmpeg string) {
	a.serveRecordedPeerCertificate(parent, conn, worker, cert, key, ffmpeg, nil, nil)
}

func (a *App) serveRecordedPeerCertificate(parent context.Context, conn *net.TCPConn, worker, cert, key, ffmpeg string, certificatePEM, keyPEM []byte) {
	defer conn.Close()
	root, err := os.MkdirTemp("", "nt-rdp-")
	if err != nil {
		return
	}
	defer os.RemoveAll(root)
	if certificatePEM != nil {
		cert = filepath.Join(root, "server.crt")
		key = filepath.Join(root, "server.key")
		if os.WriteFile(cert, certificatePEM, 0600) != nil || os.WriteFile(key, keyPEM, 0600) != nil {
			return
		}
	}
	path := filepath.Join(root, "control.sock")
	ctl, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return
	}
	defer ctl.Close()
	sup, err := rdpworker.New(worker, []string{"--record-view", path, cert, key}, 1, 5*time.Second, 2*time.Second)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	var tasks sync.WaitGroup
	tasks.Add(1)
	go func() {
		defer tasks.Done()
		peer, e := ctl.AcceptUnix()
		if e != nil {
			return
		}
		defer peer.Close()
		opt := rdprecording.Options{Root: root, FFmpeg: ffmpeg, MaxBytes: 1 << 30, IOTimeout: 5 * time.Second}
		_ = sup.ServeControlFactory(ctx, peer, a.nativeRDPRecordingFactory(ctx, root, &opt))
	}()
	_ = sup.Run(ctx, conn)
	cancel()
	ctl.Close()
	tasks.Wait()
}
