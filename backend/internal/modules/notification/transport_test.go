package notification

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSMTPRequiresSTARTTLSAndDeliversAfterUpgrade(t *testing.T) {
	certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer certServer.Close()
	for _, offer := range []bool{false, true} {
		t.Run(strconv.FormatBool(offer), func(t *testing.T) {
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			done := make(chan string, 1)
			go func() {
				conn, e := listener.Accept()
				if e != nil {
					done <- ""
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				reader := bufio.NewReader(conn)
				_, _ = conn.Write([]byte("220 fixture ESMTP\r\n"))
				secured := false
				for {
					line, e := reader.ReadString('\n')
					if e != nil {
						done <- ""
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"):
						if offer && !secured {
							_, _ = conn.Write([]byte("250-fixture\r\n250 STARTTLS\r\n"))
						} else {
							_, _ = conn.Write([]byte("250 fixture\r\n"))
						}
					case strings.HasPrefix(line, "STARTTLS"):
						_, _ = conn.Write([]byte("220 Ready\r\n"))
						secure := tls.Server(conn, certServer.TLS)
						if secure.Handshake() != nil {
							done <- ""
							return
						}
						conn = secure
						reader = bufio.NewReader(conn)
						secured = true
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						if !secured {
							done <- "cleartext envelope"
							return
						}
						_, _ = conn.Write([]byte("250 OK\r\n"))
					case strings.HasPrefix(line, "DATA"):
						_, _ = conn.Write([]byte("354 Send message\r\n"))
						var message strings.Builder
						for {
							line, e = reader.ReadString('\n')
							if e != nil {
								done <- ""
								return
							}
							if line == ".\r\n" {
								break
							}
							message.WriteString(line)
						}
						_, _ = conn.Write([]byte("250 accepted\r\n"))
						done <- message.String()
						return
					default:
						_, _ = conn.Write([]byte("250 OK\r\n"))
					}
				}
			}()
			host, port, _ := net.SplitHostPort(listener.Addr().String())
			number, _ := strconv.Atoi(port)
			ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certServer.TLS.Certificates[0].Certificate[0]}))
			e = sendSMTP(context.Background(), Config{Host: host, Port: number, From: "fixture@example.test", CA: ca}, "", "recipient@example.test", "stable-id", "test subject", "test body")
			if offer && e != nil {
				t.Fatal(e)
			}
			if !offer && e == nil {
				t.Fatal("plaintext downgrade accepted")
			}
			message := <-done
			if offer && (!strings.Contains(message, "Message-ID: <stable-id@next-terminal.invalid>") || !strings.Contains(message, "test body")) {
				t.Fatal("message missing", message)
			}
			if !offer && message != "" {
				t.Fatal(message)
			}
		})
	}
}

func TestWeComTLSAndProviderFailure(t *testing.T) {
	calls := 0
	reject := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			if r.URL.Query().Get("corpsecret") != "test-secret" {
				t.Error("missing credential")
			}
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"local-token"}`))
		case "/cgi-bin/message/send":
			if r.URL.Query().Get("access_token") != "local-token" {
				t.Error("missing access token")
			}
			if reject {
				_, _ = w.Write([]byte(`{"errcode":0,"invaliduser":"removed"}`))
			} else {
				_, _ = w.Write([]byte(`{"errcode":0}`))
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	cert, e := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	config := Config{APIBase: server.URL, CorpID: "internal", AgentID: 1, CA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))}
	if e = sendWeCom(context.Background(), config, "test-secret", "operator", "test"); e != nil {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	reject = true
	if e = sendWeCom(context.Background(), config, "test-secret", "removed", "test"); e == nil {
		t.Fatal("invalid recipient accepted")
	}
	config.CA = ""
	if e = sendWeCom(context.Background(), config, "test-secret", "operator", "test"); e == nil {
		t.Fatal("untrusted TLS accepted")
	}
}
func TestQuietWindowCrossesMidnight(t *testing.T) {
	now := time.Date(2026, 10, 29, 15, 45, 30, 0, time.UTC)
	p := Preference{QuietStart: "22:00", QuietEnd: "08:00", Timezone: "Asia/Shanghai"}
	expected := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	if got := QuietUntil(p, now); !got.Equal(expected) {
		t.Fatalf("%v != %v", got, expected)
	}
	if got := QuietUntil(p, expected); !got.IsZero() {
		t.Fatal(got)
	}
}
