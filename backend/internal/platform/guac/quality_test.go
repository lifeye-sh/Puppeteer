package guac

import (
	"reflect"
	"strings"
	"testing"
)

func TestDisplayQualityBounds(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		depth, smooth string
	}{
		{"economy", 1280, 720, "16", "false"}, {"balanced", 1920, 1080, "24", "false"}, {"sharp", 3840, 2160, "24", "true"}, {"", 3840, 2160, "24", "false"},
	} {
		q, ok := DisplayQuality(tc.name)
		if !ok {
			t.Fatal(tc.name)
		}
		values := map[string]string{"ignore-cert": "false", "disable-copy": "true", "password": "secret"}
		q.Apply(values)
		if values["color-depth"] != tc.depth || values["enable-font-smoothing"] != tc.smooth || values["ignore-cert"] != "false" || values["disable-copy"] != "true" || values["password"] != "secret" {
			t.Fatal("quality changed channel/security policy")
		}
		w, h := q.ClampSize(3840, 2160)
		if w != tc.width || h != tc.height {
			t.Fatal(tc.name, w, h)
		}
		w, h = q.ClampSize(1200, 2000)
		if w > q.Width || h > q.Height || w*2000 > h*1200+8000 {
			t.Fatal("portrait aspect lost", w, h)
		}
	}
	if _, ok := DisplayQuality("invalid"); ok {
		t.Fatal("unknown quality accepted")
	}
}

func TestEncodingEquivalentAndReusable(t *testing.T) {
	buffer := make([]byte, 0, 64*1024)
	for _, parts := range [][]string{{"name", "中文😀;,."}, {"blob", "1", strings.Repeat("A", 100000)}, {"sync", "42"}, {"name", ""}} {
		buffer = AppendEncode(buffer[:0], parts...)
		if string(buffer) != Encode(parts...) || len(buffer) != EncodedLen(parts...) {
			t.Fatal("wire differs")
		}
		decoded, err := NewReader(strings.NewReader(string(buffer))).Read()
		if err != nil || !reflect.DeepEqual(decoded, parts) {
			t.Fatal(decoded, err)
		}
	}
}

var encodedBenchmark int

func BenchmarkGraphicsEncoding(b *testing.B) {
	parts := []string{"blob", "1", strings.Repeat("A", 32768)}
	b.Run("previous", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			size := len(Encode(parts...))
			payload := append([]byte(nil), Encode(parts...)...)
			encodedBenchmark = size + len(payload)
		}
	})
	b.Run("reused", func(b *testing.B) {
		b.ReportAllocs()
		payload := make([]byte, 0, 64*1024)
		for b.Loop() {
			size := EncodedLen(parts...)
			payload = AppendEncode(payload[:0], parts...)
			encodedBenchmark = size + len(payload)
		}
	})
}
