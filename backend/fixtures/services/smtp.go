package main

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"log"
	"net"
	"strings"
	"time"
)

func serveSMTP(config *tls.Config, conf config, box *inbox) {
	listener, e := net.Listen("tcp", ":2525")
	if e != nil {
		log.Fatal(e)
	}
	for {
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		go smtpConnection(conn, config, conf, box)
	}
}
func smtpConnection(conn net.Conn, config *tls.Config, conf config, box *inbox) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReaderSize(conn, 8192)
	send := func(text string) { _, _ = conn.Write([]byte(text + "\r\n")) }
	send("220 internal-fixture ESMTP")
	secured, authenticated := false, false
	for {
		line, e := reader.ReadString('\n')
		if e != nil || len(line) > 8192 {
			return
		}
		command := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			if !secured {
				send("250-internal-fixture\r\n250 STARTTLS")
			} else {
				send("250-internal-fixture\r\n250 AUTH PLAIN")
			}
		case command == "STARTTLS" && !secured:
			send("220 Begin TLS")
			secure := tls.Server(conn, config)
			if secure.Handshake() != nil {
				return
			}
			conn = secure
			reader = bufio.NewReader(conn)
			secured = true
		case strings.HasPrefix(command, "AUTH PLAIN ") && secured:
			raw, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(command, "AUTH PLAIN "))
			parts := strings.Split(string(raw), "\x00")
			if e != nil || len(parts) != 3 || parts[1] != "fixture" || parts[2] != conf.Secret {
				send("535 Authentication failed")
				continue
			}
			authenticated = true
			send("235 authenticated")
		case strings.HasPrefix(command, "MAIL FROM:"), strings.HasPrefix(command, "RCPT TO:"):
			if !secured || !authenticated {
				send("530 authentication required")
			} else {
				send("250 accepted")
			}
		case command == "DATA":
			if !authenticated {
				send("530 authentication required")
				continue
			}
			box.mu.Lock()
			fail := box.FailSMTP
			box.mu.Unlock()
			if fail {
				send("451 internal fixture failure")
				continue
			}
			send("354 message follows")
			var message strings.Builder
			for {
				line, e = reader.ReadString('\n')
				if e != nil || message.Len()+len(line) > 1<<20 {
					return
				}
				if line == ".\r\n" {
					break
				}
				message.WriteString(line)
			}
			box.add("smtp", message.String())
			send("250 queued")
		case command == "QUIT":
			send("221 bye")
			return
		default:
			send("250 ok")
		}
	}
}
