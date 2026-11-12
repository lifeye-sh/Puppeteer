// Package guac implements bounded Guacamole instructions. Credentials are used
// only during the server-side handshake and must never be recorded or forwarded.
package guac

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxInstruction = 262144

var ErrProtocol = errors.New("invalid guacamole instruction")

type Reader struct{ r *bufio.Reader }

func NewReader(r io.Reader) *Reader { return &Reader{bufio.NewReader(r)} }
func (r *Reader) Read() ([]string, error) {
	out := []string{}
	total := 0
	for len(out) < 256 {
		n := 0
		digits := 0
		for {
			b, e := r.r.ReadByte()
			if e != nil {
				return nil, e
			}
			total++
			if b == '.' {
				break
			}
			if b < '0' || b > '9' || digits >= 6 {
				return nil, ErrProtocol
			}
			digits++
			n = n*10 + int(b-'0')
		}
		if digits == 0 || n > MaxInstruction || total+n > MaxInstruction {
			return nil, ErrProtocol
		}
		var b strings.Builder
		for i := 0; i < n; i++ {
			c, size, e := r.r.ReadRune()
			if e != nil {
				return nil, e
			}
			if c == utf8.RuneError && size == 1 {
				return nil, ErrProtocol
			}
			total += size
			if total > MaxInstruction {
				return nil, ErrProtocol
			}
			b.WriteRune(c)
		}
		out = append(out, b.String())
		sep, e := r.r.ReadByte()
		if e != nil {
			return nil, e
		}
		total++
		if total > MaxInstruction {
			return nil, ErrProtocol
		}
		if sep == ';' {
			if out[0] == "" {
				return nil, ErrProtocol
			}
			return out, nil
		}
		if sep != ',' {
			return nil, ErrProtocol
		}
	}
	return nil, ErrProtocol
}
func Encode(parts ...string) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(utf8.RuneCountInString(p)))
		b.WriteByte('.')
		b.WriteString(p)
	}
	b.WriteByte(';')
	return b.String()
}

// Handshake returns the same reader, preserving any buffered display data.
func Handshake(c io.ReadWriter, protocol string, values map[string]string) (*Reader, error) {
	if protocol != "rdp" && protocol != "vnc" {
		return nil, ErrProtocol
	}
	write := func(p ...string) error { _, e := io.WriteString(c, Encode(p...)); return e }
	if e := write("select", protocol); e != nil {
		return nil, e
	}
	r := NewReader(c)
	args, e := r.Read()
	if e != nil {
		return nil, e
	}
	if len(args) < 2 || args[0] != "args" {
		return nil, ErrProtocol
	}
	for _, p := range [][]string{{"size", "1280", "720", "96"}, {"audio"}, {"video"}, {"image", "image/png", "image/jpeg"}} {
		if e = write(p...); e != nil {
			return nil, e
		}
	}
	connect := []string{"connect"}
	for _, k := range args[1:] {
		v := values[k]
		if strings.HasPrefix(k, "VERSION_") {
			v = "VERSION_1_1_0"
		}
		connect = append(connect, v)
	}
	if e = write(connect...); e != nil {
		return nil, e
	}
	ready, e := r.Read()
	if e != nil {
		return nil, e
	}
	if len(ready) != 2 || ready[0] != "ready" {
		return nil, ErrProtocol
	}
	return r, nil
}

// InputAllowed is deliberately closed: clipboard, file, pipe, audio, object and
// reconnect instructions cannot bypass readonly or create unrecorded channels.
func InputAllowed(p []string, readonly bool) bool {
	if len(p) == 0 {
		return false
	}
	number := func(s string, max int64) bool {
		v, e := strconv.ParseInt(s, 10, 64)
		return e == nil && v >= 0 && v <= max
	}
	switch p[0] {
	case "sync":
		return len(p) == 2 && number(p[1], 1<<53)
	case "nop", "disconnect":
		return len(p) == 1
	case "ack":
		return len(p) == 4 && number(p[1], 65535) && len(p[2]) <= 256 && number(p[3], 65535)
	case "key":
		return !readonly && len(p) == 3 && number(p[1], 0x1fffffff) && (p[2] == "0" || p[2] == "1")
	case "mouse":
		return !readonly && len(p) == 4 && number(p[1], 8192) && number(p[2], 8192) && number(p[3], 31)
	case "size":
		return !readonly && len(p) == 3 && number(p[1], 3840) && number(p[2], 2160) && p[1] != "0" && p[2] != "0"
	}
	return false
}
