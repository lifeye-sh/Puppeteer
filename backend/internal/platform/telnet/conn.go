// Package telnet implements bounded RFC 854 negotiation and RFC 1073 sizing.
package telnet

import (
	"bufio"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

const (
	iac  = 255
	will = 251
	wont = 252
	do   = 253
	dont = 254
	sb   = 250
	se   = 240
)

type Conn struct {
	net.Conn
	reader        *bufio.Reader
	mu            sync.Mutex
	local, remote [256]bool
	cols, rows    int
	skipLF        bool
	readCR        bool
}

func New(c net.Conn) *Conn { return &Conn{Conn: c, reader: bufio.NewReader(c), cols: 120, rows: 32} }
func escape(p []byte) []byte {
	out := make([]byte, 0, len(p))
	for _, b := range p {
		out = append(out, b)
		if b == iac {
			out = append(out, b)
		}
	}
	return out
}
func (c *Conn) send(p []byte) error {
	_ = c.Conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	for len(p) > 0 {
		n, e := c.Conn.Write(p)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}
func (c *Conn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]byte, 0, len(p)*2)
	for _, b := range p {
		if !c.local[0] {
			if c.skipLF {
				c.skipLF = false
				if b == '\n' {
					continue
				}
			}
			if b == '\r' {
				out = append(out, '\r', '\n')
				c.skipLF = true
				continue
			}
		}
		out = append(out, b)
		if b == iac {
			out = append(out, b)
		}
	}
	if e := c.send(out); e != nil {
		return 0, e
	}
	return len(p), nil
}
func (c *Conn) size() error {
	p := []byte{31, byte(c.cols >> 8), byte(c.cols), byte(c.rows >> 8), byte(c.rows)}
	out := append([]byte{iac, sb}, escape(p)...)
	return c.send(append(out, iac, se))
}
func (c *Conn) Resize(cols, rows int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cols, c.rows = cols, rows
	if c.local[31] {
		return c.size()
	}
	return nil
}
func (c *Conn) negotiate(verb, option byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch verb {
	case will:
		accept := option == 0 || option == 1 || option == 3
		if accept && !c.remote[option] {
			c.remote[option] = true
			return c.send([]byte{iac, do, option})
		}
		if !accept {
			return c.send([]byte{iac, dont, option})
		}
	case wont:
		if c.remote[option] {
			c.remote[option] = false
			return c.send([]byte{iac, dont, option})
		}
	case do:
		accept := option == 0 || option == 3 || option == 24 || option == 31
		if accept && !c.local[option] {
			c.local[option] = true
			if e := c.send([]byte{iac, will, option}); e != nil {
				return e
			}
			if option == 31 {
				return c.size()
			}
		}
		if !accept {
			return c.send([]byte{iac, wont, option})
		}
	case dont:
		if c.local[option] {
			c.local[option] = false
			return c.send([]byte{iac, wont, option})
		}
	}
	return nil
}
func (c *Conn) subnegotiation() error {
	data := make([]byte, 0, 64)
	for {
		b, e := c.reader.ReadByte()
		if e != nil {
			return e
		}
		if b == iac {
			b, e = c.reader.ReadByte()
			if e != nil {
				return e
			}
			if b == se {
				break
			}
			if b != iac {
				return errors.New("invalid telnet subnegotiation")
			}
		}
		if len(data) >= 1024 {
			return errors.New("telnet subnegotiation too large")
		}
		data = append(data, b)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(data) == 2 && data[0] == 24 && data[1] == 1 && c.local[24] {
		return c.send(append(append([]byte{iac, sb, 24, 0}, []byte("xterm-256color")...), iac, se))
	}
	return nil
}
func (c *Conn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := 0
	for {
		b, e := c.reader.ReadByte()
		if e != nil {
			return n, e
		}
		if b == iac {
			verb, e := c.reader.ReadByte()
			if e != nil {
				return n, e
			}
			switch verb {
			case iac:
				b = iac
			case will, wont, do, dont:
				option, e := c.reader.ReadByte()
				if e != nil {
					return n, e
				}
				if e = c.negotiate(verb, option); e != nil {
					return n, e
				}
				continue
			case sb:
				if e = c.subnegotiation(); e != nil {
					return n, e
				}
				continue
			case 236:
				return n, io.EOF
			default:
				continue
			}
		}
		c.mu.Lock()
		binary := c.remote[0]
		c.mu.Unlock()
		if !binary && c.readCR && b == 0 {
			c.readCR = false
			continue
		}
		c.readCR = b == '\r'
		p[n] = b
		n++
		if n == len(p) || c.reader.Buffered() == 0 {
			return n, nil
		}
	}
}
