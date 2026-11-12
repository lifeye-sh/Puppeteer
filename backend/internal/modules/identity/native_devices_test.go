package identity

import (
	"strings"
	"testing"
)

func TestNativeDeviceDisplayMetadata(t *testing.T) {
	for _, name := range []string{strings.Repeat("字", 81), "bad\nname", "spoof\u202Ename", "bad\x00name", string([]byte{0xff})} {
		v := NativeDeviceInfo{Name: name}
		if v.Normalize() == nil {
			t.Fatal("unsafe display name accepted")
		}
	}
	for _, name := range []string{"", "办公电脑", strings.Repeat("字", 80)} {
		v := NativeDeviceInfo{Name: name, OS: "windows", Arch: "amd64", ClientVersion: "0.1.0-r14"}
		if e := v.Normalize(); e != nil {
			t.Fatal(e)
		}
	}
	for _, part := range []string{"windows\n", strings.Repeat("x", 41), "<script>", "a b"} {
		v := NativeDeviceInfo{OS: part}
		if v.Normalize() == nil {
			t.Fatal("unsafe platform identifier accepted")
		}
	}
}
