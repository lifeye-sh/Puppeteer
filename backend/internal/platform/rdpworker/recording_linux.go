//go:build linux

package rdpworker

import (
	"context"
	"net"
	"os"
	"time"

	"next-terminal/backend/internal/platform/rdprecording"
)

// RecordingReceiver accepts the authenticated worker's separate bulk stream.
// Failure closes its target bridge, including a live outbound target socket.
type RecordingReceiver struct {
	Path     string
	Done     <-chan error
	accepted <-chan struct{}
	cancel   context.CancelFunc
}

type recordingInput struct{ *net.UnixConn }

func (r recordingInput) Close() error { return r.CloseRead() }

func NewRecordingReceiver(parent context.Context, root string, peer WorkerIdentity, opt rdprecording.Options, onFailure func()) (*RecordingReceiver, error) {
	listener, dir, path, err := openWorkerSocket(root, peer, "frames.sock")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan error, 1)
	accepted := make(chan struct{})
	go func() {
		defer cancel()
		defer close(done)
		defer os.Remove(dir)
		defer listener.Close()
		stop := context.AfterFunc(ctx, func() { listener.Close() })
		defer stop()
		run := func() error {
			if err := listener.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
				return err
			}
			conn, err := acceptWorker(listener, peer)
			if err != nil {
				return err
			}
			listener.Close()
			defer conn.Close()
			close(accepted)
			send := func(v byte) error {
				if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
					return err
				}
				_, err := conn.Write([]byte{v})
				return err
			}
			opt.Ready = func() error { return send('R') }
			if _, err = rdprecording.Record(ctx, recordingInput{conn}, opt); err != nil {
				return err
			}
			return send('C')
		}
		err := run()
		if err != nil && onFailure != nil {
			onFailure()
		}
		done <- err
	}()
	return &RecordingReceiver{Path: path, Done: done, accepted: accepted, cancel: cancel}, nil
}

func (r *RecordingReceiver) Close() { r.cancel() }

// The upstream socket may close on logout before the final frames reach disk.
// No new target traffic is permitted while waiting for that bounded drain.
func (r *RecordingReceiver) Drain(ctx context.Context) bool {
	select {
	case <-r.accepted:
	default:
		r.Close()
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case err := <-r.Done:
		return err == nil
	case <-ctx.Done():
		r.Close()
	case <-timer.C:
		r.Close()
	}
	return false
}
