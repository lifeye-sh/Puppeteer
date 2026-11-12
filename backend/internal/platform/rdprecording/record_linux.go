//go:build linux

// Package rdprecording records the bounded full-frame stream of a native RDP
// worker. It does not authenticate a worker or expose a production RDP listener.
package rdprecording

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const FPS = 10
const segmentFrames = 100
const maxFrames = 4 * 60 * 60 * FPS
const maxSegments = 2048
const maxPixels = 1920 * 1080 * 4

var ErrProtocol = errors.New("invalid native recording frame stream")
var ErrEncoder = errors.New("native recording encoder failed")
var ErrQuota = errors.New("native recording video quota exceeded")
var ErrInputTimeout = errors.New("native recording frame timeout")

type Options struct {
	// Discard validates/acknowledges frames without opening files or an encoder.
	Discard bool
	// Root is an existing private, service-owned directory, never worker input.
	Root, SessionID, FFmpeg string
	// MaxBytes limits sealed and in-progress video bytes. The bounded manifest
	// is separate (at most 1 MiB). The caller must enforce aggregate spool limits.
	MaxBytes  int64
	IOTimeout time.Duration
	// Ready is called after the initial private manifest has been committed.
	Ready func() error
	// FirstFrame records platform activation after the encoder accepts a frame.
	FirstFrame func(context.Context) error
	// Publish commits a sealed segment to the audit store before acknowledging it.
	Publish func(context.Context, int, Segment, string) error
}
type Segment struct {
	File          string `json:"file"`
	SHA256        string `json:"sha256"`
	Bytes         int64  `json:"bytes"`
	StartMS       int64  `json:"startMs"`
	DurationMS    int64  `json:"durationMs"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	EncodedWidth  int    `json:"encodedWidth"`
	EncodedHeight int    `json:"encodedHeight"`
	Frames        int    `json:"frames"`
}
type Manifest struct {
	Version    int       `json:"version"`
	SessionID  string    `json:"sessionId"`
	State      string    `json:"state"`
	Reason     string    `json:"reason,omitempty"`
	FPS        int       `json:"fps"`
	Frames     uint64    `json:"frames"`
	Bytes      int64     `json:"bytes"`
	DurationMS int64     `json:"durationMs"`
	Segments   []Segment `json:"segments"`
}
type frame struct {
	width, height int
	pixels        []byte
	end           bool
}

// NTRF v1: 24-byte big-endian header: magic[4], version[1], kind[1],
// reserved[2], sequence[8], width[2], height[2], payloadBytes[4]. Kind 1 is
// tightly packed BGRA32, kind 2 is an explicit end with no dimensions/payload.
// Sequence starts at zero and advances for every 100 ms sample, even on an
// unchanged screen. Missing samples are errors, never silently filled in.
func WriteFrame(w io.Writer, sequence uint64, width, height int, pixels []byte) error {
	if width < 2 || height < 2 || width > 1920 || height > 1080 || len(pixels) != width*height*4 {
		return ErrProtocol
	}
	return writePacket(w, sequence, width, height, pixels, 1)
}
func WriteEnd(w io.Writer, sequence uint64) error { return writePacket(w, sequence, 0, 0, nil, 2) }
func writePacket(w io.Writer, sequence uint64, width, height int, pixels []byte, kind byte) error {
	var header [24]byte
	copy(header[:4], "NTRF")
	header[4] = 1
	header[5] = kind
	binary.BigEndian.PutUint64(header[8:16], sequence)
	binary.BigEndian.PutUint16(header[16:18], uint16(width))
	binary.BigEndian.PutUint16(header[18:20], uint16(height))
	binary.BigEndian.PutUint32(header[20:24], uint32(len(pixels)))
	for _, part := range [][]byte{header[:], pixels} {
		for len(part) > 0 {
			n, e := w.Write(part)
			if e != nil {
				return e
			}
			if n <= 0 {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}
func readFrame(r io.Reader, sequence uint64) (frame, error) {
	var h [24]byte
	if _, e := io.ReadFull(r, h[:]); e != nil {
		return frame{}, ErrProtocol
	}
	if string(h[:4]) != "NTRF" || h[4] != 1 || h[6] != 0 || h[7] != 0 || binary.BigEndian.Uint64(h[8:16]) != sequence {
		return frame{}, ErrProtocol
	}
	w, hgt, n := int(binary.BigEndian.Uint16(h[16:18])), int(binary.BigEndian.Uint16(h[18:20])), uint64(binary.BigEndian.Uint32(h[20:24]))
	if h[5] == 2 {
		if w != 0 || hgt != 0 || n != 0 || sequence == 0 {
			return frame{}, ErrProtocol
		}
		return frame{end: true}, nil
	}
	if h[5] != 1 || w < 2 || hgt < 2 || w > 1920 || hgt > 1080 || n > maxPixels || n != uint64(w*hgt*4) || sequence >= maxFrames {
		return frame{}, ErrProtocol
	}
	p := make([]byte, int(n))
	if _, e := io.ReadFull(r, p); e != nil {
		clear(p)
		return frame{}, ErrProtocol
	}
	return frame{width: w, height: hgt, pixels: p}, nil
}

// Record owns input. Its Close must interrupt Read (an OS pipe or net.Conn).
// Only an explicit end and successful sealing can produce a complete manifest.
// Failure discards the current segment; previously sealed segments remain valid.
func Record(ctx context.Context, input io.ReadCloser, opt Options) (manifest Manifest, err error) {
	if input == nil {
		return manifest, errors.New("missing native recording input")
	}
	defer input.Close()
	id, e := uuid.Parse(opt.SessionID)
	if e != nil || id.String() != opt.SessionID || !filepath.IsAbs(opt.Root) || !filepath.IsAbs(opt.FFmpeg) || opt.MaxBytes < 1 || opt.MaxBytes > 1<<40 || opt.IOTimeout < time.Millisecond || opt.IOTimeout > 15*time.Second {
		return manifest, errors.New("invalid native recorder configuration")
	}
	if opt.Discard {
		return discardStream(ctx, input, opt)
	}
	st, e := os.Lstat(opt.Root)
	if e != nil || !st.IsDir() || st.Mode().Perm()&0077 != 0 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return manifest, errors.New("recorder root must be a private directory")
	}
	dir := filepath.Join(opt.Root, opt.SessionID)
	if e = os.Mkdir(dir, 0700); e != nil {
		return manifest, e
	}
	manifest = Manifest{Version: 1, SessionID: opt.SessionID, State: "writing", FPS: FPS, Segments: []Segment{}}
	if e = saveManifest(dir, manifest); e != nil {
		return manifest, e
	}
	// Pdeathsig is attached to the creating Linux thread. Keep it alive until
	// all encoders have been reaped, including cancellation and failure paths.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var enc *encoder
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(work, func() { input.Close() })
	defer stop()
	defer func() {
		if enc != nil {
			if copyErr := enc.abort(); errors.Is(copyErr, ErrQuota) || (errors.Is(err, ErrEncoder) && copyErr != nil && !errors.Is(copyErr, os.ErrClosed)) {
				err = copyErr
			}
		}
		if err != nil {
			manifest.State = "incomplete"
			manifest.Reason = reason(err)
			if e := saveManifest(dir, manifest); e != nil {
				err = errors.Join(err, errors.New("recording failure manifest could not be persisted"))
			}
		}
	}()
	if opt.Ready != nil {
		if err = opt.Ready(); err != nil {
			return manifest, err
		}
	}
	seal := func() error {
		if enc == nil {
			return nil
		}
		segment, e := enc.finish()
		enc = nil
		if e != nil {
			if work.Err() != nil {
				return work.Err()
			}
			return e
		}
		if opt.Publish != nil {
			path := filepath.Join(dir, segment.File)
			e = opt.Publish(work, len(manifest.Segments)+1, segment, path)
			removeErr := os.Remove(path)
			if e != nil {
				return e
			}
			if removeErr != nil {
				return removeErr
			}
		}
		manifest.Segments = append(manifest.Segments, segment)
		manifest.Frames += uint64(segment.Frames)
		manifest.Bytes += segment.Bytes
		manifest.DurationMS += segment.DurationMS
		return saveManifest(dir, manifest)
	}
	for sequence := uint64(0); ; sequence++ {
		if e := work.Err(); e != nil {
			return manifest, e
		}
		timedOut := make(chan struct{})
		timer := time.AfterFunc(opt.IOTimeout, func() { close(timedOut); input.Close() })
		f, e := readFrame(input, sequence)
		if !timer.Stop() {
			<-timedOut
			clear(f.pixels)
			return manifest, ErrInputTimeout
		}
		if e != nil {
			if work.Err() != nil {
				return manifest, work.Err()
			}
			return manifest, e
		}
		if f.end {
			if e = seal(); e != nil {
				return manifest, e
			}
			manifest.State = "complete"
			if e = saveManifest(dir, manifest); e != nil {
				return manifest, e
			}
			return manifest, nil
		}
		if enc != nil && (enc.frames == segmentFrames || enc.width != f.width || enc.height != f.height) {
			if e = seal(); e != nil {
				clear(f.pixels)
				return manifest, e
			}
		}
		if enc == nil {
			if len(manifest.Segments) >= maxSegments {
				clear(f.pixels)
				return manifest, ErrQuota
			}
			enc, e = startEncoder(work, opt, dir, len(manifest.Segments), sequence, f.width, f.height, opt.MaxBytes-manifest.Bytes)
			if e != nil {
				clear(f.pixels)
				return manifest, e
			}
		}
		e = enc.write(f.pixels)
		clear(f.pixels)
		if e != nil {
			if work.Err() != nil {
				return manifest, work.Err()
			}
			return manifest, e
		}
		if sequence == 0 && opt.FirstFrame != nil {
			if e = opt.FirstFrame(work); e != nil {
				return manifest, e
			}
		}
		if enc.frames == segmentFrames {
			if e = seal(); e != nil {
				return manifest, e
			}
		}
	}
}
func reason(e error) string {
	switch {
	case errors.Is(e, context.Canceled), errors.Is(e, context.DeadlineExceeded):
		return "cancelled"
	case errors.Is(e, ErrQuota):
		return "quota_exceeded"
	case errors.Is(e, ErrInputTimeout):
		return "frame_timeout"
	case errors.Is(e, ErrProtocol):
		return "invalid_frame_stream"
	case errors.Is(e, ErrEncoder):
		return "encoder_failed"
	default:
		return "storage_failed"
	}
}
func saveManifest(dir string, m Manifest) error {
	b, e := json.Marshal(m)
	if e != nil {
		return e
	}
	if len(b) > 1<<20 {
		return ErrQuota
	}
	path := filepath.Join(dir, "manifest.next")
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer os.Remove(path)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(path, filepath.Join(dir, "manifest.json")); e != nil {
		return e
	}
	d, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}

type encoder struct {
	cmd                   *exec.Cmd
	cancel                context.CancelFunc
	stdin                 *os.File
	file                  *os.File
	copyDone              chan struct{}
	copyErr               error
	stopIO                func() bool
	part, final           string
	width, height, frames int
	start                 uint64
	timeout               time.Duration
	bytes                 int64
}
type quotaWriter struct {
	file      *os.File
	remaining int64
	bytes     *int64
}

func (w *quotaWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > w.remaining {
		return 0, ErrQuota
	}
	n, e := w.file.Write(b)
	w.remaining -= int64(n)
	*w.bytes += int64(n)
	return n, e
}
func startEncoder(ctx context.Context, opt Options, dir string, index int, start uint64, w, h int, remaining int64) (*encoder, error) {
	if remaining <= 0 {
		return nil, ErrQuota
	}
	part := filepath.Join(dir, fmt.Sprintf("segment-%06d.part", index))
	final := filepath.Join(dir, fmt.Sprintf("segment-%06d.mp4", index))
	f, e := os.OpenFile(part, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return nil, e
	}
	work, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(work, opt.FFmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-probesize", "32", "-fpsprobesize", "0", "-threads", "1", "-filter_threads", "1", "-f", "rawvideo", "-pixel_format", "bgra", "-video_size", fmt.Sprintf("%dx%d", w, h), "-framerate", "10", "-i", "pipe:0", "-an", "-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2", "-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency", "-g", "10", "-crf", "23", "-threads", "1", "-pix_fmt", "yuv420p", "-movflags", "+frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1")
	cmd.Env = []string{"LANG=C"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	cmd.Stderr = io.Discard
	stdin, e := cmd.StdinPipe()
	if e != nil {
		cancel()
		f.Close()
		os.Remove(part)
		return nil, ErrEncoder
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		cancel()
		stdin.Close()
		f.Close()
		os.Remove(part)
		return nil, ErrEncoder
	}
	if e = cmd.Start(); e != nil {
		cancel()
		stdin.Close()
		stdout.Close()
		f.Close()
		os.Remove(part)
		return nil, ErrEncoder
	}
	v := &encoder{cmd: cmd, cancel: cancel, stdin: stdin.(*os.File), file: f, copyDone: make(chan struct{}), part: part, final: final, width: w, height: h, start: start, timeout: opt.IOTimeout}
	v.stopIO = context.AfterFunc(work, func() { stdin.Close(); stdout.Close() })
	go func() {
		_, e := io.Copy(&quotaWriter{file: f, remaining: remaining, bytes: &v.bytes}, stdout)
		v.copyErr = e
		if e != nil {
			cancel()
		}
		close(v.copyDone)
	}()
	return v, nil
}
func (e *encoder) write(b []byte) error {
	if e.stdin.SetWriteDeadline(time.Now().Add(e.timeout)) != nil {
		return ErrEncoder
	}
	for len(b) > 0 {
		n, err := e.stdin.Write(b)
		if err != nil || n <= 0 {
			return ErrEncoder
		}
		b = b[n:]
	}
	e.frames++
	return nil
}
func (e *encoder) abort() error {
	defer e.stopIO()
	e.cancel()
	e.stdin.Close()
	_ = e.cmd.Wait()
	<-e.copyDone
	e.file.Close()
	_ = os.Remove(e.part)
	return e.copyErr
}
func (e *encoder) finish() (segment Segment, err error) {
	defer e.cancel()
	defer e.stopIO()
	defer e.file.Close()
	defer os.Remove(e.part)
	e.stdin.Close()
	timer := time.AfterFunc(e.timeout, e.cancel)
	defer timer.Stop()
	<-e.copyDone
	copyErr := e.copyErr
	waitErr := e.cmd.Wait()
	if copyErr != nil {
		if errors.Is(copyErr, os.ErrClosed) {
			return segment, ErrEncoder
		}
		return segment, copyErr
	}
	if waitErr != nil || e.bytes == 0 {
		return segment, ErrEncoder
	}
	if err = e.file.Sync(); err != nil {
		return segment, err
	}
	if _, err = e.file.Seek(0, io.SeekStart); err != nil {
		return segment, err
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, e.file); err != nil {
		return segment, err
	}
	if err = os.Rename(e.part, e.final); err != nil {
		return segment, err
	}
	return Segment{File: filepath.Base(e.final), SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: e.bytes, StartMS: int64(e.start) * 100, DurationMS: int64(e.frames) * 100, Width: e.width, Height: e.height, EncodedWidth: (e.width + 1) &^ 1, EncodedHeight: (e.height + 1) &^ 1, Frames: e.frames}, nil
}

// A disabled audit still validates the worker stream and activates the session
// only after a complete first frame. Frame bytes are cleared immediately.
func discardStream(ctx context.Context, input io.ReadCloser, opt Options) (Manifest, error) {
	m := Manifest{Version: 1, SessionID: opt.SessionID, State: "disabled", FPS: FPS, Segments: []Segment{}}
	stop := context.AfterFunc(ctx, func() { input.Close() })
	defer stop()
	if opt.Ready != nil {
		if e := opt.Ready(); e != nil {
			return m, e
		}
	}
	for sequence := uint64(0); ; sequence++ {
		if e := ctx.Err(); e != nil {
			return m, e
		}
		timedOut := make(chan struct{})
		timer := time.AfterFunc(opt.IOTimeout, func() { close(timedOut); input.Close() })
		f, e := readFrame(input, sequence)
		clear(f.pixels)
		if !timer.Stop() {
			<-timedOut
			return m, ErrInputTimeout
		}
		if e != nil {
			if ctx.Err() != nil {
				return m, ctx.Err()
			}
			return m, e
		}
		if f.end {
			return m, nil
		}
		if sequence == 0 && opt.FirstFrame != nil {
			if e = opt.FirstFrame(ctx); e != nil {
				return m, e
			}
		}
		m.Frames++
		m.DurationMS += 100
	}
}
