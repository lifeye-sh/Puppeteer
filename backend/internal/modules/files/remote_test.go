package files

import (
	"testing"
)

func TestNormalizeBoundaries(t *testing.T) {
	for _, v := range []string{"../x", "/a/../x", "a\\b", "a\x00b", ".nt-upload-x", "a/.nt-upload-x", "a\nb"} {
		if _, e := Normalize(v); e == nil {
			t.Fatalf("accepted %q", v)
		}
	}
	for input, want := range map[string]string{"": "", "/": "", "中文/文件.txt": "中文/文件.txt", "/a/./b": "a/b"} {
		got, e := Normalize(input)
		if e != nil || got != want {
			t.Fatalf("%q -> %q %v", input, got, e)
		}
	}
}
