package mysqlproxy

import (
	"encoding/binary"
	"errors"
	"io"
)

const MaxPacketBytes = 64 << 20
const packetFragment = 1<<24 - 1

var ErrPacket = errors.New("invalid or oversized MySQL packet")

// Packet reads validate the announced length before allocating. A fragmented
// message shares one budget; a chain of maximum-size fragments cannot evade it.
func readPacket(r io.Reader, sequence *byte, limit int) ([]byte, error) {
	var payload []byte
	for {
		var header [4]byte
		if _, e := io.ReadFull(r, header[:]); e != nil {
			return nil, e
		}
		size := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
		if header[3] != *sequence || size > limit-len(payload) {
			return nil, ErrPacket
		}
		*sequence++
		start := len(payload)
		payload = append(payload, make([]byte, size)...)
		if _, e := io.ReadFull(r, payload[start:]); e != nil {
			return nil, e
		}
		if size < packetFragment {
			return payload, nil
		}
	}
}
func writePacket(w io.Writer, sequence *byte, payload []byte) error {
	for {
		size := min(len(payload), packetFragment)
		header := []byte{byte(size), byte(size >> 8), byte(size >> 16), *sequence}
		*sequence++
		if e := writeAll(w, header); e != nil {
			return e
		}
		if e := writeAll(w, payload[:size]); e != nil {
			return e
		}
		payload = payload[size:]
		if size < packetFragment {
			return nil
		}
	}
}
func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, e := w.Write(data)
		if e != nil {
			return e
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
func lengthEncoded(data []byte) (uint64, int, error) {
	if len(data) == 0 {
		return 0, 0, ErrPacket
	}
	switch data[0] {
	case 0xfc:
		if len(data) < 3 {
			return 0, 0, ErrPacket
		}
		return uint64(binary.LittleEndian.Uint16(data[1:3])), 3, nil
	case 0xfd:
		if len(data) < 4 {
			return 0, 0, ErrPacket
		}
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16, 4, nil
	case 0xfe:
		if len(data) < 9 {
			return 0, 0, ErrPacket
		}
		return binary.LittleEndian.Uint64(data[1:9]), 9, nil
	case 0xfb, 0xff:
		return 0, 0, ErrPacket
	default:
		return uint64(data[0]), 1, nil
	}
}
