package guac

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
)

func TestClipboardLimitsAndIsolation(t *testing.T) {
	var c Clipboard
	c.Handle([]string{"clipboard", "12", "text/plain"})
	h, _, _ := c.Handle([]string{"blob", "13", "aGVsbG8="})
	if h {
		t.Fatal("image stream consumed")
	}
	h, _, reply := c.Handle([]string{"blob", "12", base64.StdEncoding.EncodeToString([]byte("你好"))})
	if !h || reply[3] != "0" {
		t.Fatal("valid text rejected")
	}
	_, text, _ := c.Handle([]string{"end", "12"})
	if string(text) != "你好" {
		t.Fatal("text mismatch")
	}
	c.Handle([]string{"clipboard", "12", "text/html"})
	c.Handle([]string{"blob", "12", "aGVsbG8="})
	_, text, _ = c.Handle([]string{"end", "12"})
	if text != nil {
		t.Fatal("html allowed")
	}
	c.Handle([]string{"clipboard", "12", "text/plain"})
	c.Handle([]string{"blob", "12", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", ClipboardLimit+1)))})
	_, text, _ = c.Handle([]string{"end", "12"})
	if text != nil {
		t.Fatal("oversize accepted")
	}
	for _, s := range []string{"!", base64.StdEncoding.EncodeToString([]byte{255}), base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", ClipboardLimit+1)))} {
		if _, ok := ClipboardInput(s); ok {
			t.Fatal("invalid input accepted")
		}
	}
	if _, ok := ClipboardInput("aGVsbG8="); !ok {
		t.Fatal("valid input rejected")
	}
}

func TestClipboardInputStreamRangeAndRoundtrip(t *testing.T) {
	for _, text := range []string{"", "中文\r\nsecond line", strings.Repeat("x", ClipboardLimit)} {
		parts, ok := ClipboardInput(base64.StdEncoding.EncodeToString([]byte(text)))
		if !ok || len(parts) != 3 {
			t.Fatal("valid clipboard rejected")
		}
		r := NewReader(strings.NewReader(strings.Join(parts, "")))
		var receiver Clipboard
		for i := 0; i < 3; i++ {
			p, err := r.Read()
			if err != nil {
				t.Fatal(err)
			}
			index, err := strconv.Atoi(p[1])
			if err != nil || index < 0 || index >= 512 {
				t.Fatal("guacd rejects stream index", p[1])
			}
			_, got, _ := receiver.Handle(p)
			if i == 2 && string(got) != text {
				t.Fatal("clipboard changed in transit")
			}
		}
	}
}
