package telnet

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestNegotiationFragmentationAndSizing(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	_ = a.SetDeadline(time.Now().Add(3 * time.Second))
	_ = b.SetDeadline(time.Now().Add(3 * time.Second))
	c := New(a)
	done := make(chan error, 1)
	go func() {
		_, e := b.Write([]byte{iac, will, 1})
		if e != nil {
			done <- e
			return
		}
		reply := make([]byte, 3)
		_, e = io.ReadFull(b, reply)
		if e != nil || !bytes.Equal(reply, []byte{iac, do, 1}) {
			done <- io.ErrUnexpectedEOF
			return
		}
		_, _ = b.Write([]byte{iac, do, 31})
		reply = make([]byte, 12)
		_, e = io.ReadFull(b, reply)
		if e != nil || !bytes.Equal(reply, []byte{iac, will, 31, iac, sb, 31, 0, 120, 0, 32, iac, se}) {
			done <- io.ErrUnexpectedEOF
			return
		}
		for _, v := range []byte{'a', iac, iac, '\r', 0, 'b'} {
			if _, e = b.Write([]byte{v}); e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	out := make([]byte, 5)
	n, e := io.ReadFull(c, out[:4])
	if e != nil || n != 4 || !bytes.Equal(out[:4], []byte{'a', 255, '\r', 'b'}) {
		t.Fatalf("output %v %v", out, e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	go func() {
		reply := make([]byte, 10)
		_, e := io.ReadFull(b, reply)
		if e == nil && !bytes.Equal(reply, []byte{iac, sb, 31, 0, 255, 255, 0, 40, iac, se}) {
			e = io.ErrUnexpectedEOF
		}
		done <- e
	}()
	if e = c.Resize(255, 40); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
func TestNVTInputEscapesAndSubnegotiationLimit(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	_ = a.SetDeadline(time.Now().Add(3 * time.Second))
	_ = b.SetDeadline(time.Now().Add(3 * time.Second))
	c := New(a)
	done := make(chan []byte, 1)
	go func() { p := make([]byte, 7); _, _ = io.ReadFull(b, p); done <- p }()
	_, e := c.Write([]byte{'a', '\r'})
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.Write([]byte{'\n', 'b', 255, 'c'})
	if e != nil {
		t.Fatal(e)
	}
	if p := <-done; !bytes.Equal(p, []byte{'a', '\r', '\n', 'b', 255, 255, 'c'}) {
		t.Fatal(p)
	}
	go func() { _, _ = b.Write(append([]byte{iac, sb}, bytes.Repeat([]byte{1}, 1025)...)) }()
	if _, e = c.Read(make([]byte, 10)); e == nil {
		t.Fatal("unbounded subnegotiation accepted")
	}
}
