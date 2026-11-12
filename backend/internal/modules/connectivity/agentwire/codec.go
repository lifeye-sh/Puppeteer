// Package agentwire implements bounded nt-agent.v1 records shared by gateway and agent.
package agentwire

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const MaxMessage = 64 * 1024
const (
	Data  byte = 1
	Fin   byte = 2
	Reset byte = 3
)

var ErrProtocol = errors.New("invalid agent protocol record")

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, e := w.Write(b)
		if n < 0 || n > len(b) {
			return io.ErrShortWrite
		}
		b = b[n:]
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// ReadJSON validates the advertised length before allocating any payload.
func ReadJSON(r io.Reader, dst any) error {
	var h [4]byte
	if _, e := io.ReadFull(r, h[:]); e != nil {
		return e
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > MaxMessage {
		return ErrProtocol
	}
	b := make([]byte, int(n))
	if _, e := io.ReadFull(r, b); e != nil {
		return e
	}
	if !utf8.Valid(b) {
		return ErrProtocol
	}
	return json.Unmarshal(b, dst)
}
func WriteJSON(w io.Writer, value any) error {
	b, e := json.Marshal(value)
	if e != nil {
		return e
	}
	if len(b) == 0 || len(b) > MaxMessage {
		return ErrProtocol
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	if e = writeAll(w, h[:]); e != nil {
		return e
	}
	return writeAll(w, b)
}
func validRecord(kind byte, n uint32) bool {
	switch kind {
	case Data:
		return n > 0 && n <= MaxMessage
	case Fin:
		return n == 0
	case Reset:
		return n > 0 && n <= 1024
	}
	return false
}

// Decoder tracks one direction. FIN/RST is terminal: callers must not accept
// further records on that direction. The other direction may finish independently.
type Decoder struct{ ended bool }

func (d *Decoder) Read(r io.Reader) (byte, []byte, error) {
	if d.ended {
		return 0, nil, ErrProtocol
	}
	var h [5]byte
	if _, e := io.ReadFull(r, h[:]); e != nil {
		return 0, nil, e
	}
	n := binary.BigEndian.Uint32(h[1:])
	if !validRecord(h[0], n) {
		return 0, nil, ErrProtocol
	}
	b := make([]byte, int(n))
	if _, e := io.ReadFull(r, b); e != nil {
		return 0, nil, e
	}
	if h[0] == Reset && (!utf8.Valid(b) || !json.Valid(b)) {
		return 0, nil, ErrProtocol
	}
	d.ended = h[0] == Fin || h[0] == Reset
	return h[0], b, nil
}
func WriteRecord(w io.Writer, kind byte, b []byte) error {
	if len(b) > MaxMessage || !validRecord(kind, uint32(len(b))) {
		return ErrProtocol
	}
	if kind == Reset && (!utf8.Valid(b) || !json.Valid(b)) {
		return ErrProtocol
	}
	var h [5]byte
	h[0] = kind
	binary.BigEndian.PutUint32(h[1:], uint32(len(b)))
	if e := writeAll(w, h[:]); e != nil {
		return e
	}
	return writeAll(w, b)
}
