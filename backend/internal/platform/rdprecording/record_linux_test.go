//go:build linux

package rdprecording

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
)

func options(t *testing.T) Options {
	t.Helper()
	ff, e := exec.LookPath("ffmpeg")
	if e != nil {
		if os.Getenv("NT_REQUIRE_RDP_RECORDER") == "1" {
			t.Fatal("ffmpeg required")
		}
		t.Skip("ffmpeg unavailable")
	}
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	return Options{Root: root, SessionID: uuid.NewString(), FFmpeg: ff, MaxBytes: 8 << 20, IOTimeout: 3 * time.Second}
}
func pixels(w, h int, red byte) []byte {
	b := make([]byte, w*h*4)
	for i := 0; i < len(b); i += 4 {
		b[i+2] = red
		b[i+3] = 255
	}
	return b
}
func stream(t *testing.T, count int, end bool) []byte {
	t.Helper()
	var b bytes.Buffer
	for i := 0; i < count; i++ {
		if e := WriteFrame(&b, uint64(i), 64, 48, pixels(64, 48, 220)); e != nil {
			t.Fatal(e)
		}
	}
	if end {
		if e := WriteEnd(&b, uint64(count)); e != nil {
			t.Fatal(e)
		}
	}
	return b.Bytes()
}
func persisted(t *testing.T, opt Options) Manifest {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(opt.Root, opt.SessionID, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	var m Manifest
	if e = json.Unmarshal(b, &m); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(filepath.Join(opt.Root, opt.SessionID))
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range entries {
		if strings.HasSuffix(f.Name(), ".part") || f.Name() == "manifest.next" {
			t.Fatalf("temporary file left: %s", f.Name())
		}
		st, _ := f.Info()
		if st.Mode().Perm() != 0600 {
			t.Fatalf("unsafe file permissions %s: %v", f.Name(), st.Mode())
		}
	}
	return m
}
func verifySegment(t *testing.T, opt Options, s Segment) {
	t.Helper()
	path := filepath.Join(opt.Root, opt.SessionID, s.File)
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(b)
	if int64(len(b)) != s.Bytes || hex.EncodeToString(sum[:]) != s.SHA256 {
		t.Fatal("segment integrity mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, e = exec.CommandContext(ctx, "ffprobe", "-v", "error", "-count_frames", "-show_entries", "stream=codec_name,width,height,nb_read_frames,pix_fmt,r_frame_rate:format=duration", "-of", "json", path).Output()
	if e != nil {
		t.Fatalf("ffprobe: %v", e)
	}
	var p struct {
		Streams []struct {
			Codec         string `json:"codec_name"`
			Width, Height int
			Frames        string `json:"nb_read_frames"`
			Pixel         string `json:"pix_fmt"`
			Rate          string `json:"r_frame_rate"`
		}
		Format struct {
			Duration string `json:"duration"`
		}
	}
	if e = json.Unmarshal(b, &p); e != nil {
		t.Fatal(e)
	}
	if len(p.Streams) != 1 {
		t.Fatalf("unexpected streams: %s", b)
	}
	v := p.Streams[0]
	d, _ := strconv.ParseFloat(p.Format.Duration, 64)
	if v.Codec != "h264" || v.Pixel != "yuv420p" || v.Width != s.EncodedWidth || v.Height != s.EncodedHeight || v.Frames != strconv.Itoa(s.Frames) || v.Rate != "10/1" || math.Abs(d-float64(s.DurationMS)/1000) > .01 {
		t.Fatalf("encoded output mismatch: %s; manifest %+v", b, s)
	}
	b, e = exec.CommandContext(ctx, opt.FFmpeg, "-v", "error", "-i", path, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1").Output()
	if e != nil {
		t.Fatal(e)
	}
	i := (v.Width*(v.Height/2) + v.Width/2) * 3
	if len(b) < i+3 || b[i] < 200 || b[i+1] > 20 || b[i+2] > 20 {
		t.Fatal("BGRA red frame decoded incorrectly")
	}
}
func TestRecorderSegmentsResizeAndDecode(t *testing.T) {
	opt := options(t)
	var b bytes.Buffer
	for i := 0; i < 130; i++ {
		w, h := 64, 48
		if i >= 110 {
			w, h = 65, 49
		}
		if e := WriteFrame(&b, uint64(i), w, h, pixels(w, h, 220)); e != nil {
			t.Fatal(e)
		}
	}
	if e := WriteEnd(&b, 130); e != nil {
		t.Fatal(e)
	}
	m, e := Record(context.Background(), io.NopCloser(&b), opt)
	if e != nil {
		t.Fatal(e)
	}
	if m.State != "complete" || m.Frames != 130 || m.DurationMS != 13000 || len(m.Segments) != 3 {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	for i, s := range m.Segments {
		if s.Frames != []int{100, 10, 20}[i] || s.StartMS != []int64{0, 10000, 11000}[i] {
			t.Fatalf("wrong segment boundary: %+v", s)
		}
		verifySegment(t, opt, s)
	}
	disk := persisted(t, opt)
	if disk.State != "complete" || disk.Bytes != m.Bytes {
		t.Fatal("manifest not committed")
	}
}
func TestRecorderUncleanEndKeepsOnlySealedPrefix(t *testing.T) {
	opt := options(t)
	m, e := Record(context.Background(), io.NopCloser(bytes.NewReader(stream(t, 101, false))), opt)
	if !errors.Is(e, ErrProtocol) || m.State != "incomplete" || m.Frames != 100 || m.DurationMS != 10000 || len(m.Segments) != 1 {
		t.Fatalf("unexpected unclean result: %+v %v", m, e)
	}
	verifySegment(t, opt, m.Segments[0])
	disk := persisted(t, opt)
	if disk.State != "incomplete" || disk.Reason != "invalid_frame_stream" {
		t.Fatalf("wrong persisted state: %+v", disk)
	}
}
func TestRecorderFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*Options)
		input func(*testing.T) []byte
		want  error
	}{
		{"quota", func(o *Options) { o.MaxBytes = 100 }, func(t *testing.T) []byte { return stream(t, 2, true) }, ErrQuota},
		{"encoder_missing", func(o *Options) { o.FFmpeg = "/missing/ffmpeg" }, func(t *testing.T) []byte { return stream(t, 2, true) }, ErrEncoder},
		{"encoder_exits", func(o *Options) { o.FFmpeg = "/bin/false" }, func(t *testing.T) []byte { return stream(t, 2, true) }, ErrEncoder},
		{"no_end", func(o *Options) {}, func(t *testing.T) []byte { return stream(t, 2, false) }, ErrProtocol},
		{"empty_end", func(o *Options) {}, func(t *testing.T) []byte { var b bytes.Buffer; _ = WriteEnd(&b, 0); return b.Bytes() }, ErrProtocol},
		{"truncated", func(o *Options) {}, func(t *testing.T) []byte { return stream(t, 2, false)[:30] }, ErrProtocol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := options(t)
			tc.setup(&o)
			m, e := Record(context.Background(), io.NopCloser(bytes.NewReader(tc.input(t))), o)
			if !errors.Is(e, tc.want) || m.State != "incomplete" || m.Bytes != 0 || len(m.Segments) != 0 {
				t.Fatalf("unexpected result %+v %v", m, e)
			}
			if p := persisted(t, o); p.State != "incomplete" || p.Reason != reason(tc.want) {
				t.Fatalf("wrong state %+v", p)
			}
		})
	}
}
func TestFrameHeaderRejectsBeforePayload(t *testing.T) {
	var b bytes.Buffer
	_ = WriteFrame(&b, 0, 2, 2, pixels(2, 2, 220))
	base := b.Bytes()[:24]
	for _, mutate := range []func([]byte){func(b []byte) { b[0] = 0 }, func(b []byte) { b[4] = 2 }, func(b []byte) { b[5] = 3 }, func(b []byte) { b[6] = 1 }, func(b []byte) { b[7] = 1 }, func(b []byte) { b[15] = 1 }, func(b []byte) { binary.BigEndian.PutUint16(b[16:18], 1921) }, func(b []byte) { binary.BigEndian.PutUint16(b[18:20], 1081) }, func(b []byte) { binary.BigEndian.PutUint32(b[20:24], 0xffffffff) }} {
		h := bytes.Clone(base)
		mutate(h)
		r := &headerOnly{header: bytes.NewReader(h)}
		_, e := readFrame(r, 0)
		if !errors.Is(e, ErrProtocol) || r.payloadRead {
			t.Fatal("invalid header attempted payload read")
		}
	}
	limitHeader := bytes.Clone(base)
	binary.BigEndian.PutUint64(limitHeader[8:16], maxFrames)
	r := &headerOnly{header: bytes.NewReader(limitHeader)}
	_, e := readFrame(r, maxFrames)
	if !errors.Is(e, ErrProtocol) || r.payloadRead {
		t.Fatal("frame limit not enforced")
	}
}

func TestRecorderQuotaIncludesSealedSegments(t *testing.T) {
	opt := options(t)
	first, e := Record(context.Background(), io.NopCloser(bytes.NewReader(stream(t, 100, true))), opt)
	if e != nil {
		t.Fatal(e)
	}
	opt.SessionID = uuid.NewString()
	opt.MaxBytes = first.Bytes + 100
	m, e := Record(context.Background(), io.NopCloser(bytes.NewReader(stream(t, 101, true))), opt)
	if !errors.Is(e, ErrQuota) || m.State != "incomplete" || m.Frames != 100 || m.Bytes != first.Bytes {
		t.Fatalf("aggregate video quota failed: %+v %v", m, e)
	}
	persisted(t, opt)
	verifySegment(t, opt, m.Segments[0])
}

// A separate process imposes a real kernel file-size limit on the recorder's
// writes, without changing the test runner, host filesystem, or other services.
func TestRecorderStorageFailure(t *testing.T) {
	if os.Getenv("NT_RECORDER_LIMIT_CHILD") == "1" {
		opt := options(t)
		signal.Ignore(syscall.SIGXFSZ)
		if e := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 1024, Max: 1024}); e != nil {
			t.Fatal(e)
		}
		m, e := Record(context.Background(), io.NopCloser(bytes.NewReader(stream(t, 2, true))), opt)
		if !errors.Is(e, syscall.EFBIG) || m.State != "incomplete" || m.Reason != "storage_failed" {
			t.Fatalf("failed write not propagated: %+v %v", m, e)
		}
		persisted(t, opt)
		return
	}
	_ = options(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecorderStorageFailure$", "-test.v")
	cmd.Env = append(os.Environ(), "NT_RECORDER_LIMIT_CHILD=1")
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("storage failure child: %v %s", e, b)
	}
}

type headerOnly struct {
	header      *bytes.Reader
	payloadRead bool
}

func (r *headerOnly) Read(b []byte) (int, error) {
	if r.header.Len() == 0 {
		r.payloadRead = true
		return 0, io.EOF
	}
	return r.header.Read(b)
}
func TestRecorderReadTimeoutAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelled), func(t *testing.T) {
			opt := options(t)
			opt.IOTimeout = 100 * time.Millisecond
			r, w := io.Pipe()
			defer w.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := ErrInputTimeout
			if cancelled {
				want = context.Canceled
				time.AfterFunc(20*time.Millisecond, cancel)
			}
			start := time.Now()
			m, e := Record(ctx, r, opt)
			if !errors.Is(e, want) || time.Since(start) > time.Second || m.State != "incomplete" {
				t.Fatalf("read did not stop: %+v %v", m, e)
			}
			persisted(t, opt)
		})
	}
}
func TestRecorderEncoderStallAndProcessGroup(t *testing.T) {
	for _, script := range []string{"#!/bin/sh\nexec /bin/sleep 60\n", "#!/bin/sh\n/bin/cat >/dev/null\n/bin/sleep 60 &\nwait\n"} {
		opt := options(t)
		opt.IOTimeout = 150 * time.Millisecond
		path := filepath.Join(opt.Root, "stall")
		if e := os.WriteFile(path, []byte(script), 0700); e != nil {
			t.Fatal(e)
		}
		opt.FFmpeg = path
		start := time.Now()
		m, e := Record(context.Background(), io.NopCloser(bytes.NewReader(stream(t, 2, true))), opt)
		if e == nil || time.Since(start) > 2*time.Second || m.State != "incomplete" {
			t.Fatalf("encoder did not fail promptly: %+v %v", m, e)
		}
		persisted(t, opt)
	}
}
func TestRecorderRejectsReuseAndUnsafeRoot(t *testing.T) {
	opt := options(t)
	dir := filepath.Join(opt.Root, opt.SessionID)
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	sentinel := filepath.Join(dir, "manifest.json")
	if e := os.WriteFile(sentinel, []byte("existing"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Record(context.Background(), io.NopCloser(bytes.NewReader(nil)), opt); e == nil {
		t.Fatal("existing session accepted")
	}
	b, _ := os.ReadFile(sentinel)
	if string(b) != "existing" {
		t.Fatal("existing session overwritten")
	}
	opt.SessionID = uuid.NewString()
	if e := os.Chmod(opt.Root, 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := Record(context.Background(), io.NopCloser(bytes.NewReader(nil)), opt); e == nil {
		t.Fatal("public root accepted")
	}
	opt.Root = filepath.Join(t.TempDir(), "link")
	if e := os.Symlink(filepath.Dir(dir), opt.Root); e != nil {
		t.Fatal(e)
	}
	if _, e := Record(context.Background(), io.NopCloser(bytes.NewReader(nil)), opt); e == nil {
		t.Fatal("symlink root accepted")
	}
}

func TestRecorderFullHD(t *testing.T) {
	opt := options(t)
	r, w := io.Pipe()
	producer := make(chan error, 1)
	go func() {
		defer w.Close()
		p := pixels(1920, 1080, 220)
		defer clear(p)
		for i := 0; i < 10; i++ {
			if e := WriteFrame(w, uint64(i), 1920, 1080, p); e != nil {
				producer <- e
				return
			}
		}
		producer <- WriteEnd(w, 10)
	}()
	m, e := Record(context.Background(), r, opt)
	if e != nil {
		t.Fatal(e)
	}
	if e = <-producer; e != nil {
		t.Fatal(e)
	}
	if m.State != "complete" || len(m.Segments) != 1 || m.Frames != 10 {
		t.Fatalf("unexpected full HD output %+v", m)
	}
	verifySegment(t, opt, m.Segments[0])
	persisted(t, opt)
}

func TestRecorderCancellationDuringEncoderDrain(t *testing.T) {
	opt := options(t)
	path := filepath.Join(opt.Root, "stall")
	if e := os.WriteFile(path, []byte("#!/bin/sh\n/bin/cat >/dev/null\n/bin/sleep 60\n"), 0700); e != nil {
		t.Fatal(e)
	}
	opt.FFmpeg = path
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	m, e := Record(ctx, io.NopCloser(bytes.NewReader(stream(t, 2, true))), opt)
	if !errors.Is(e, context.DeadlineExceeded) || m.Reason != "cancelled" {
		t.Fatalf("cancel lost: %+v %v", m, e)
	}
	persisted(t, opt)
}

func TestRecorderParentDeath(t *testing.T) {
	if dir := os.Getenv("NT_RECORDER_DEATH_CHILD"); dir != "" {
		opt := options(t)
		opt.Root = dir
		opt.FFmpeg = filepath.Join(dir, "encoder")
		r, w := io.Pipe()
		defer w.Close()
		go func() {
			for i := 0; i < 2; i++ {
				_ = WriteFrame(w, uint64(i), 64, 48, pixels(64, 48, 220))
			}
		}()
		_, _ = Record(context.Background(), r, opt)
		t.Fatal("test parent should have been killed")
	}
	opt := options(t)
	pidPath := filepath.Join(opt.Root, "encoder.pid")
	// Ignore stdin/EOF: otherwise the encoder could exit naturally when its
	// parent dies, which would not prove that Pdeathsig was effective.
	script := "#!/bin/sh\nprintf '%s' \"$$\" > '" + pidPath + "'\nexec /bin/sleep 60\n"
	if e := os.WriteFile(filepath.Join(opt.Root, "encoder"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecorderParentDeath$")
	cmd.Env = append(os.Environ(), "NT_RECORDER_DEATH_CHILD="+opt.Root)
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	var pid int
	until := time.Now().Add(4 * time.Second)
	for time.Now().Before(until) {
		b, _ := os.ReadFile(pidPath)
		pid, _ = strconv.Atoi(string(b))
		if pid > 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid <= 1 {
		t.Fatal("encoder child did not start")
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	if e := cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	until = time.Now().Add(time.Second)
	for time.Now().Before(until) {
		b, e := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
		if errors.Is(e, os.ErrNotExist) || strings.Contains(string(b), "State:\tZ") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("encoder remained running after recorder parent died")
}
