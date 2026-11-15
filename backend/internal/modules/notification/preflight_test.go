package notification

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"puppeteer/backend/internal/platform/configstate"
)

func TestSMTPPreflightDoesNotDeliverOrLeak(t *testing.T) {
	cert := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer cert.Close()
	for _, mode := range []string{"starttls", "implicit", "no-starttls", "bad-ca", "bad-auth", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			done := make(chan []string, 1)
			go func() {
				raw, e := l.Accept()
				if e != nil {
					done <- nil
					return
				}
				defer raw.Close()
				raw.SetDeadline(time.Now().Add(3 * time.Second))
				var conn net.Conn = raw
				secured := mode == "implicit"
				if secured {
					conn = tls.Server(raw, cert.TLS)
				}
				conn.Write([]byte("220 fixture\r\n"))
				reader := bufio.NewReader(conn)
				var commands []string
				defer func() { done <- commands }()
				for {
					line, e := reader.ReadString('\n')
					if e != nil {
						return
					}
					command := strings.Fields(line)[0]
					commands = append(commands, command)
					switch command {
					case "EHLO":
						if !secured && mode != "no-starttls" {
							conn.Write([]byte("250-fixture\r\n250 STARTTLS\r\n"))
						} else {
							conn.Write([]byte("250-fixture\r\n250 AUTH PLAIN\r\n"))
						}
					case "STARTTLS":
						conn.Write([]byte("220 ready\r\n"))
						secure := tls.Server(raw, cert.TLS)
						if secure.Handshake() != nil {
							return
						}
						conn = secure
						reader = bufio.NewReader(conn)
						secured = true
					case "AUTH":
						if !secured {
							t.Error("credential before TLS")
							return
						}
						if mode == "bad-auth" {
							conn.Write([]byte("535 provider-secret-must-never-return\r\n"))
						} else {
							conn.Write([]byte("235 authenticated\r\n"))
						}
					case "QUIT":
						conn.Write([]byte("221 bye\r\n"))
						return
					case "*": // net/smtp aborts a rejected authentication exchange.
						conn.Write([]byte("501 authentication cancelled\r\n"))
					default:
						t.Errorf("unexpected side effect %s", command)
						return
					}
				}
			}()
			host, port, _ := net.SplitHostPort(l.Addr().String())
			n, _ := strconv.Atoi(port)
			ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.TLS.Certificates[0].Certificate[0]}))
			if mode == "bad-ca" {
				ca = ""
			}
			var checks []configstate.Check
			ProbeSMTP(context.Background(), Config{Host: host, Port: n, ImplicitTLS: mode == "implicit", Username: "fixture", From: "sender@example.test", CA: ca}, "probe-secret", func(c configstate.Check) bool {
				checks = append(checks, c)
				return mode != "cancel" || c.Code != "smtp_network"
			})
			commands := <-done
			if mode == "cancel" && len(commands) != 0 {
				t.Fatal(commands)
			}
			last := checks[len(checks)-1]
			if mode == "starttls" || mode == "implicit" {
				if last.Code != "smtp_probe_complete" || last.State != "ready" {
					t.Fatal(checks)
				}
			} else if mode != "cancel" && last.State != "blocked" {
				t.Fatal(checks)
			}
			raw, _ := json.Marshal(checks)
			if strings.Contains(string(raw), "probe-secret") || strings.Contains(string(raw), "provider-secret") {
				t.Fatal("secret leaked")
			}
		})
	}
}

func TestSMTPPreflightContextStopsGreeting(t *testing.T) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := l.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(2 * time.Second))
		var b [1]byte
		c.Read(b[:])
	}()
	host, port, _ := net.SplitHostPort(l.Addr().String())
	n, _ := strconv.Atoi(port)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	ProbeSMTP(ctx, Config{Host: host, Port: n, From: "sender@example.test"}, "", func(configstate.Check) bool { return true })
	if time.Since(start) > time.Second {
		t.Fatal("cancellation did not close connection")
	}
	<-done
}
