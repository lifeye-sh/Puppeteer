// A private-network diagnostic fixture. It never executes OS commands.
package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"
)

func main() {
	ln, e := net.Listen("tcp", ":2323")
	if e != nil {
		log.Fatal(e)
	}
	for {
		c, e := ln.Accept()
		if e != nil {
			log.Fatal(e)
		}
		go serve(c)
	}
}
func serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Minute))
	_, _ = c.Write([]byte{255, 251, 1})
	_, _ = io.WriteString(c, "R07 Telnet diagnostic fixture\r\nNo OS command execution. Commands: help, whoami, hostname, pwd, exit\r\nfixture> ")
	r := bufio.NewReader(c)
	line := make([]byte, 0, 256)
	for {
		b, e := r.ReadByte()
		if e != nil {
			return
		}
		if b == 255 {
			v, e := r.ReadByte()
			if e != nil {
				return
			}
			if v >= 251 && v <= 254 {
				_, _ = r.ReadByte()
				continue
			}
			if v == 250 {
				for {
					v, e = r.ReadByte()
					if e != nil {
						return
					}
					if v == 255 {
						v, e = r.ReadByte()
						if e != nil {
							return
						}
						if v == 240 {
							break
						}
					}
				}
				continue
			}
			continue
		}
		if b == '\r' || b == 0 {
			continue
		}
		if b == '\n' {
			_, _ = io.WriteString(c, "\r\n")
			cmd := strings.TrimSpace(string(line))
			switch cmd {
			case "exit":
				return
			case "whoami":
				fmt.Fprint(c, "fixture-user\r\n")
			case "hostname":
				fmt.Fprint(c, "r07-telnet-fixture\r\n")
			case "pwd":
				fmt.Fprint(c, "/fixture\r\n")
			case "help":
				fmt.Fprint(c, "help, whoami, hostname, pwd, exit\r\n")
			default:
				fmt.Fprint(c, "Unsupported fixture command\r\n")
			}
			line = line[:0]
			_, _ = io.WriteString(c, "fixture> ")
			continue
		}
		if b == 127 || b == 8 {
			if len(line) > 0 {
				line = line[:len(line)-1]
				_, _ = io.WriteString(c, "\b \b")
			}
			continue
		}
		if len(line) >= 4096 {
			return
		}
		line = append(line, b)
		_, _ = c.Write([]byte{b})
	}
}
