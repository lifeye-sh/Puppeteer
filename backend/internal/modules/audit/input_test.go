package audit

import (
	"strings"
	"testing"
)

func submit(t *testing.T, tr *InputTracker, input string) []*Submission {
	t.Helper()
	items, e := tr.Input([]byte(input))
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range items {
		v.ID = "test-id"
	}
	return items
}
func TestTerminalEchoRecognition(t *testing.T) {
	cases := []struct{ name, input, echo, want string }{
		{"plain", "whoami\r", "whoami\r\n", "whoami"},
		{"delete", "pwdd\x7f\r", "pwd\r\n", "pwd"},
		{"unicode", "echo 中文\r", "echo 中文\r\n", "echo 中文"},
		{"insert", "eco\x1b[Dh\x05 hi\r", "echo hi\r\n", "echo hi"},
		{"erase-word", "echo bad\x17good\r", "echo good\r\n", "echo good"},
		{"history", "\x1b[A\r", "\rhost$ whoami\x1b[K\r\n", "whoami"},
		{"completion", "pw\t\r", "pwd\r\n", "pwd"},
		{"no-echo", "hidden-secret\r", "\r\n", ""},
		{"mismatch", "hidden-secret\r", "Permission denied\r\n", ""},
		{"multiline-paste", "\x1b[200~echo one\necho two\x1b[201~\r", "echo one\r\n", ""},
		{"empty", "\r", "\r\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var tr InputTracker
			tr.Output([]byte("host$ "))
			items := submit(t, &tr, tc.input)
			if len(items) != 1 {
				t.Fatalf("submissions %d", len(items))
			}
			var found []*Submission
			// Bytewise output exercises split UTF-8, ANSI and CRLF across packets.
			for _, b := range []byte(tc.echo) {
				found = append(found, tr.Output([]byte{b})...)
			}
			if items[0].Command != tc.want {
				t.Fatalf("got %q want %q", items[0].Command, tc.want)
			}
			if tc.want == "" && len(found) != 0 {
				t.Fatal("unrecognized input was promoted")
			}
			if items[0].candidate != "" {
				t.Fatal("transient input retained after correlation")
			}
		})
	}
}
func TestTerminalEditingAndApplicationBoundaries(t *testing.T) {
	var tr InputTracker
	tr.Output([]byte("host$ "))
	if items := submit(t, &tr, "echo cancelled\x03"); len(items) != 0 {
		t.Fatal("Ctrl-C submitted")
	}
	items := submit(t, &tr, "whoami\r\n")
	if len(items) != 1 {
		t.Fatal("CRLF duplicated")
	}
	tr.Output([]byte("whoami\r\n"))
	if items[0].Command != "whoami" {
		t.Fatal(items[0])
	}
	tr.Output([]byte("\x1b[?1049h"))
	items = submit(t, &tr, "editor text\r")
	if len(tr.Output([]byte("editor text\r\n"))) != 0 || items[0].Command != "" {
		t.Fatal("editor text treated as command")
	}
	tr.Output([]byte("\x1b[?1049lhost$ "))
	items = submit(t, &tr, "pwd\r")
	tr.Output([]byte("pwd\r\n"))
	if items[0].Command != "pwd" {
		t.Fatal("failed after leaving full screen")
	}
}
func TestTerminalBatchedInputAndLimits(t *testing.T) {
	var tr InputTracker
	tr.Output([]byte("host$ "))
	items := submit(t, &tr, "echo a\necho b\n")
	tr.Output([]byte("echo a\r\necho b\r\n"))
	if len(items) != 2 || items[0].Command != "echo a" || items[1].Command != "echo b" {
		t.Fatal("lost batched submissions")
	}
	if _, e := tr.Input([]byte(strings.Repeat("\n", 101))); e == nil {
		t.Fatal("unbounded submission batch")
	}
	var huge InputTracker
	huge.Output([]byte("host$ "))
	items = submit(t, &huge, strings.Repeat("a", 9000)+"\r")
	huge.Output([]byte(strings.Repeat("a", 9000) + "\r\n"))
	if items[0].Command != "" {
		t.Fatal("truncated command was misrepresented")
	}
}
