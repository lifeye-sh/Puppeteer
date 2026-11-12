package guac

import (
	"encoding/base64"
	"strconv"
	"unicode/utf8"
)

const ClipboardLimit = 16 * 1024

// Client input streams must be below GUAC_USER_MAX_STREAMS (512 in guacd 1.6).
// This relay owns all client-created streams; native file/clipboard streams
// from the browser are disallowed. Server output streams use a separate pool.
const ClipboardInputStream = "0"

// Clipboard intercepts only negotiated text streams, never image/file streams.
// Clipboard content is delivered separately and must not enter the recording.
type Clipboard struct {
	id      string
	data    []byte
	discard bool
}

func (c *Clipboard) Handle(p []string) (handled bool, text []byte, reply []string) {
	if len(p) == 0 {
		return
	}
	if p[0] == "clipboard" {
		handled = true
		if len(p) != 3 {
			return
		}
		id, e := strconv.ParseUint(p[1], 10, 16)
		if e != nil {
			return
		}
		_ = id
		c.id = p[1]
		c.data = nil
		c.discard = p[2] != "text/plain"
		if c.discard {
			reply = []string{"ack", c.id, "Only plain text is supported", "781"}
		}
		return
	}
	if len(p) < 2 || c.id == "" || p[1] != c.id {
		return
	}
	if p[0] == "blob" {
		handled = true
		reply = []string{"ack", c.id, "OK", "0"}
		if len(p) != 3 {
			c.discard = true
		}
		if !c.discard {
			b, e := base64.StdEncoding.DecodeString(p[2])
			if e != nil || len(c.data)+len(b) > ClipboardLimit {
				c.discard = true
				c.data = nil
			} else {
				c.data = append(c.data, b...)
			}
		}
		if c.discard {
			reply = []string{"ack", c.id, "Clipboard limit exceeded", "781"}
		}
		return
	}
	if p[0] == "end" {
		handled = true
		if !c.discard && utf8.Valid(c.data) {
			text = append([]byte{}, c.data...)
		}
		c.id = ""
		c.data = nil
		c.discard = false
		return
	}
	return
}
func ClipboardInput(encoded string) ([]string, bool) {
	b, e := base64.StdEncoding.DecodeString(encoded)
	if e != nil || len(b) > ClipboardLimit || !utf8.Valid(b) {
		return nil, false
	}
	return []string{Encode("clipboard", ClipboardInputStream, "text/plain"), Encode("blob", ClipboardInputStream, encoded), Encode("end", ClipboardInputStream)}, true
}
