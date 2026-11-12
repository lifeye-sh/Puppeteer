// Private diagnostic RFB fixture: draws color blocks, never runs an OS desktop.
package main

import (
	"encoding/binary"
	"io"
	"log"
	"net"
	"time"
)

func main() {
	l, e := net.Listen("tcp", ":5900")
	if e != nil {
		log.Fatal(e)
	}
	for {
		c, e := l.Accept()
		if e != nil {
			return
		}
		go serve(c)
	}
}
func serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Minute))
	read := func(n int) ([]byte, error) { b := make([]byte, n); _, e := io.ReadFull(c, b); return b, e }
	write := func(b []byte) error { _, e := c.Write(b); return e }
	if write([]byte("RFB 003.008\n")) != nil {
		return
	}
	if _, e := read(12); e != nil {
		return
	}
	if write([]byte{1, 1}) != nil {
		return
	}
	if b, e := read(1); e != nil || b[0] != 1 {
		return
	}
	write([]byte{0, 0, 0, 0})
	if _, e := read(1); e != nil {
		return
	}
	name := []byte("R07 VNC diagnostic - click or type to change colors")
	format := []byte{32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0}
	header := make([]byte, 24)
	binary.BigEndian.PutUint16(header, 640)
	binary.BigEndian.PutUint16(header[2:], 360)
	copy(header[4:], format)
	binary.BigEndian.PutUint32(header[20:], uint32(len(name)))
	if write(append(header, name...)) != nil {
		return
	}
	tone := byte(0)
	for {
		b, e := read(1)
		if e != nil {
			return
		}
		switch b[0] {
		case 0:
			b, e = read(19)
			if e != nil {
				return
			}
			format = append([]byte{}, b[3:]...)
			if format[0] != 32 || format[3] != 1 {
				return
			}
		case 2:
			b, e = read(3)
			if e != nil {
				return
			}
			n := int(binary.BigEndian.Uint16(b[1:]))
			if n > 256 {
				return
			}
			if _, e = read(n * 4); e != nil {
				return
			}
		case 3:
			if _, e = read(9); e != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
			packet := make([]byte, 16+640*360*4)
			packet[3] = 1
			binary.BigEndian.PutUint16(packet[8:], 640)
			binary.BigEndian.PutUint16(packet[10:], 360)
			for y := 0; y < 360; y++ {
				for x := 0; x < 640; x++ {
					red := byte(30 + x/4)
					green := byte(40 + y/2)
					blue := byte(90 + tone)
					if x > 60 && x < 580 && y > 100 && y < 260 {
						red = 30 + tone
						green = 140
						blue = 210
					}
					value := uint32(red)<<format[10] | uint32(green)<<format[11] | uint32(blue)<<format[12]
					i := 16 + (y*640+x)*4
					if format[2] == 0 {
						binary.LittleEndian.PutUint32(packet[i:], value)
					} else {
						binary.BigEndian.PutUint32(packet[i:], value)
					}
				}
			}
			if write(packet) != nil {
				return
			}
		case 4:
			if _, e = read(7); e != nil {
				return
			}
			tone += 11
		case 5:
			b, e = read(5)
			if e != nil {
				return
			}
			if b[0] != 0 {
				tone += 17
			}
		case 6:
			b, e = read(7)
			if e != nil {
				return
			}
			n := binary.BigEndian.Uint32(b[3:])
			if n > 65536 {
				return
			}
			if _, e = read(int(n)); e != nil {
				return
			}
		default:
			return
		}
	}
}
