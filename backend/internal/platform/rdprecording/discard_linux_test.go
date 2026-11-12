//go:build linux

package rdprecording

import (
	"bytes"
	"context"
	"errors"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscardFramesNeverStartsEncoderOrWritesFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "must-not-exist")
	ready, first := 0, 0
	opt := Options{Discard: true, Root: root, SessionID: uuid.NewString(), FFmpeg: "/nonexistent-encoder", MaxBytes: 1, IOTimeout: time.Second, Ready: func() error { ready++; return nil }, FirstFrame: func(context.Context) error { first++; return nil }, Publish: func(context.Context, int, Segment, string) error { t.Fatal("discard published video"); return nil }}
	m, e := Record(context.Background(), io.NopCloser(bytes.NewReader(stream(t, 3, true))), opt)
	if e != nil || m.State != "disabled" || m.Bytes != 0 || m.Frames != 3 || len(m.Segments) != 0 || ready != 1 || first != 1 {
		t.Fatal(m, e, ready, first)
	}
	if _, e = os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("discard touched recording filesystem", e)
	}
	bad := stream(t, 2, true)
	bad[15] = 1
	if _, e = Record(context.Background(), io.NopCloser(bytes.NewReader(bad)), opt); !errors.Is(e, ErrProtocol) {
		t.Fatal("malformed frame accepted", e)
	}
	r, w := io.Pipe()
	defer w.Close()
	opt.IOTimeout = 10 * time.Millisecond
	if _, e = Record(context.Background(), r, opt); !errors.Is(e, ErrInputTimeout) {
		t.Fatal("unbounded idle reader", e)
	}
}
