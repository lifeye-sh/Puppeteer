package agentwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(b []byte) (int, error) {
	if len(b) > 2 {
		b = b[:2]
	}
	return w.Buffer.Write(b)
}
func TestJSONBoundaries(t *testing.T) {
	for _, n := range []uint32{0, MaxMessage + 1, 0xffffffff} {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], n)
		var dst any
		if !errors.Is(ReadJSON(bytes.NewReader(b[:]), &dst), ErrProtocol) {
			t.Fatal(n)
		}
	}
	var w shortWriter
	if e := WriteJSON(&w, map[string]string{"type": "heartbeat", "name": "内网"}); e != nil {
		t.Fatal(e)
	}
	var dst map[string]string
	if e := ReadJSON(&w, &dst); e != nil || dst["name"] != "内网" {
		t.Fatal(dst, e)
	}
	var b bytes.Buffer
	_ = WriteJSON(&b, map[string]int{"v": 1})
	raw := b.Bytes()
	var out any
	if e := ReadJSON(bytes.NewReader(raw[:len(raw)-1]), &out); !errors.Is(e, io.ErrUnexpectedEOF) {
		t.Fatal(e)
	}
}
func TestRecordLifecycle(t *testing.T) {
	var w shortWriter
	for _, v := range []struct {
		k byte
		b []byte
	}{{Data, []byte("hello")}, {Fin, nil}, {Data, []byte("late")}} {
		if e := WriteRecord(&w, v.k, v.b); e != nil {
			t.Fatal(e)
		}
	}
	var d Decoder
	k, b, e := d.Read(&w)
	if e != nil || k != Data || string(b) != "hello" {
		t.Fatal(k, b, e)
	}
	k, _, e = d.Read(&w)
	if e != nil || k != Fin {
		t.Fatal(k, e)
	}
	if _, _, e = d.Read(&w); !errors.Is(e, ErrProtocol) {
		t.Fatal("accepted data after FIN")
	}
}
func TestMalformedRecords(t *testing.T) {
	for _, v := range []struct {
		k byte
		n uint32
	}{{9, 0}, {Fin, 1}, {Data, MaxMessage + 1}, {Reset, 1025}, {Data, 0}} {
		var h [5]byte
		h[0] = v.k
		binary.BigEndian.PutUint32(h[1:], v.n)
		var d Decoder
		if _, _, e := d.Read(bytes.NewReader(h[:])); !errors.Is(e, ErrProtocol) {
			t.Fatal(v, e)
		}
	}
	var b bytes.Buffer
	if e := WriteRecord(&b, Reset, []byte("not-json")); !errors.Is(e, ErrProtocol) {
		t.Fatal(e)
	}
}
