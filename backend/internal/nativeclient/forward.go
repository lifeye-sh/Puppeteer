package nativeclient

import (
	"context"
	"errors"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"sync"
)

// ServeForward accepts only a numeric loopback listener. Neither tokens nor
// target credentials are sent to local applications. Closing the SSH transport
// or cancelling the context closes all accepted sockets and the listener.
func ServeForward(parent context.Context, peer *ssh.Client, listener net.Listener, target string) error {
	return serveForward(parent, peer, listener, target, false)
}

// ServeForwardOnce consumes the first local connection and closes the listener.
// Loopback is not an authentication boundary: another local process can win
// the first connection. Target protocol authentication remains required.
func ServeForwardOnce(parent context.Context, peer *ssh.Client, listener net.Listener, target string) error {
	return serveForward(parent, peer, listener, target, true)
}

func serveForward(parent context.Context, peer *ssh.Client, listener net.Listener, target string, once bool) error {
	host, _, err := net.SplitHostPort(listener.Addr().String())
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("forward listener must be loopback")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer listener.Close()
	var workers sync.WaitGroup
	var onceErr error

	stop := context.AfterFunc(ctx, func() { listener.Close(); peer.Close() })
	defer func() { cancel(); listener.Close(); peer.Close(); workers.Wait(); stop() }()
	go func() { _ = peer.Wait(); cancel() }()
	slots := make(chan struct{}, 8)
	for {
		local, e := listener.Accept()
		if e != nil {
			if ctx.Err() != nil {
				if once {
					if parent.Err() != nil {
						return parent.Err()
					}
					return errors.New("SSH bridge closed before local connection")
				}
				return nil
			}
			return e
		}
		select {
		case slots <- struct{}{}:
		default:
			local.Close()
			continue
		}
		if once {
			_ = listener.Close()
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-slots }()
			defer local.Close()
			closeLocal := context.AfterFunc(ctx, func() { local.Close() })
			defer closeLocal()
			remote, e := peer.DialContext(ctx, "tcp", target)
			if e != nil {
				if once {
					onceErr = e
				}
				return
			}
			defer remote.Close()
			done := make(chan error, 1)
			go func() {
				_, uploadErr := io.Copy(remote, local)
				if c, ok := remote.(interface{ CloseWrite() error }); ok {
					uploadErr = errors.Join(uploadErr, c.CloseWrite())
				}
				if uploadErr != nil {
					local.Close()
					remote.Close()
				}
				done <- uploadErr
			}()
			_, copyErr := io.Copy(local, remote)
			if c, ok := local.(*net.TCPConn); ok {
				_ = c.CloseWrite()
			}
			if copyErr != nil {
				local.Close()
				remote.Close()
			}
			uploadErr := <-done
			if once {
				onceErr = errors.Join(uploadErr, copyErr)
			}
		}()
		if once {
			workers.Wait()
			if parent.Err() != nil {
				return parent.Err()
			}
			if ctx.Err() != nil {
				return errors.Join(onceErr, errors.New("SSH bridge disconnected during forwarding"))
			}
			return onceErr
		}
	}
}
