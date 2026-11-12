package agentwire

import (
	"io"
	"net"
	"sync"
	"time"
)

type Dial struct {
	V              int    `json:"v"`
	Type           string `json:"type"`
	RequestID      string `json:"requestId"`
	SessionID      string `json:"sessionId"`
	Epoch          int64  `json:"epoch"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Deadline       int64  `json:"deadlineUnixMs"`
	LeaseExpiresAt int64  `json:"leaseExpiresAt"`
}
type DialResult struct {
	Type       string `json:"type"`
	RequestID  string `json:"requestId"`
	OK         bool   `json:"ok"`
	ErrorCode  string `json:"errorCode,omitempty"`
	ResolvedIP string `json:"resolvedIp,omitempty"`
}
type Lease struct {
	SessionID string `json:"sessionId"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	ExpiresAt int64  `json:"leaseExpiresAt"`
}

// Conn preserves TCP half-close explicitly, independent of yamux semantics.
type Conn struct {
	net.Conn
	deadlineMu        sync.Mutex
	writeDeadline     time.Time
	readMu, writeMu   sync.Mutex
	decoder           Decoder
	pending           []byte
	readEOF, writeEOF bool
	Once              sync.Once
	OnClose           func()
}

func (c *Conn) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if len(b) == 0 {
		return 0, nil
	}
	if c.readEOF {
		return 0, io.EOF
	}
	if len(c.pending) == 0 {
		k, p, e := c.decoder.Read(c.Conn)
		if e != nil {
			return 0, e
		}
		if k == Fin {
			c.readEOF = true
			return 0, io.EOF
		}
		if k != Data {
			return 0, net.ErrClosed
		}
		c.pending = p
	}
	n := copy(b, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}
func (c *Conn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.writeEOF {
		return 0, net.ErrClosed
	}
	n := 0
	for len(b) > 0 {
		size := len(b)
		if size > MaxMessage {
			size = MaxMessage
		}
		c.applyWriteDeadline()
		if e := WriteRecord(c.Conn, Data, b[:size]); e != nil {
			return n, e
		}
		b = b[size:]
		n += size
	}
	return n, nil
}
func (c *Conn) CloseWrite() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.writeEOF {
		return nil
	}
	c.writeEOF = true
	c.applyWriteDeadline()
	return WriteRecord(c.Conn, Fin, nil)
}
func (c *Conn) Close() error {
	c.Once.Do(func() {
		if c.OnClose != nil {
			c.OnClose()
		}
	})
	return c.Conn.Close()
}
func (c *Conn) SetDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	c.writeDeadline = t
	return c.Conn.SetDeadline(t)
}
func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	c.writeDeadline = t
	return c.Conn.SetWriteDeadline(t)
}
func (c *Conn) applyWriteDeadline() {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	deadline := time.Now().Add(15 * time.Second)
	if !c.writeDeadline.IsZero() && c.writeDeadline.Before(deadline) {
		deadline = c.writeDeadline
	}
	_ = c.Conn.SetWriteDeadline(deadline)
}
