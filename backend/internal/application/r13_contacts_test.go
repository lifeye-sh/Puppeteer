package application

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/notification"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func contactSMTPFixture(t *testing.T, config *tls.Config, messages chan string) (string, int) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			raw, e := listener.Accept()
			if e != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer raw.Close()
				raw.SetDeadline(time.Now().Add(5 * time.Second))
				var conn net.Conn = raw
				reader := bufio.NewReader(conn)
				io.WriteString(conn, "220 fixture ESMTP\r\n")
				secured := false
				for {
					line, e := reader.ReadString('\n')
					if e != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"):
						if !secured {
							io.WriteString(conn, "250-fixture\r\n250 STARTTLS\r\n")
						} else {
							io.WriteString(conn, "250 fixture\r\n")
						}
					case strings.HasPrefix(line, "STARTTLS"):
						io.WriteString(conn, "220 Ready\r\n")
						c := tls.Server(conn, config)
						if c.Handshake() != nil {
							return
						}
						conn = c
						reader = bufio.NewReader(c)
						secured = true
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						if !secured {
							t.Error("cleartext mail")
							return
						}
						io.WriteString(conn, "250 OK\r\n")
					case strings.HasPrefix(line, "DATA"):
						io.WriteString(conn, "354 Send\r\n")
						var body strings.Builder
						for {
							line, e = reader.ReadString('\n')
							if e != nil {
								return
							}
							if line == ".\r\n" {
								break
							}
							body.WriteString(line)
						}
						messages <- body.String()
						io.WriteString(conn, "250 accepted\r\n")
						return
					default:
						io.WriteString(conn, "250 OK\r\n")
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { listener.Close(); wg.Wait() })
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	number, _ := strconv.Atoi(port)
	return host, number
}
func TestR13VerifiedContactsRecoveryAndSMS(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	const secure = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, secure)
	admin := loginAs(t, srv, "accept-admin")
	changeLoginDuration(t, srv, admin, 7)
	uid := postID(t, srv, "/api/admin/users", userInput("contact-user", nil, nil), admin)
	owner := loginAs(t, srv, "contact-user")
	postID(t, srv, "/api/admin/users", userInput("contact-other", nil, nil), admin)
	other := loginAs(t, srv, "contact-other")
	certificate, ca := ldapLifecycleCertificate(t)
	messages := make(chan string, 16)
	config := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	host, port := contactSMTPFixture(t, config, messages)
	vendor := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sms-fixture-secret" || r.Header.Get("Idempotency-Key") == "" {
			t.Error("missing SMS proof")
		}
		var payload map[string]string
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["to"] != "+8613800000000" {
			t.Error("SMS recipient")
		}
		messages <- payload["text"]
		writeJSON(w, 200, map[string]bool{"accepted": true})
	}))
	vendor.TLS = config
	vendor.StartTLS()
	defer vendor.Close()
	email := postID(t, srv, "/api/admin/notifications/channels", notification.Channel{Name: "verification email", Type: "smtp", Enabled: true, Config: notification.Config{Host: host, Port: port, CA: ca, From: "security@example.test"}}, admin)
	sms := postID(t, srv, "/api/admin/notifications/channels", notification.Channel{Name: "verification SMS", Type: "sms-webhook", Enabled: true, Secret: "sms-fixture-secret", Config: notification.Config{APIBase: vendor.URL, CA: ca}}, admin)
	settings := map[string]any{"emailChannel": email, "smsChannel": sms, "recoveryEnabled": true, "smsLoginEnabled": true, "version": 1, "password": testPassword}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/contact-settings", settings, admin, origin), 200)
	reset := func() {
		a.mu.Lock()
		a.attempts = map[string]attempt{}
		a.mu.Unlock()
		if _, e := pool.Exec(ctx, `UPDATE contact_challenges SET created_at=now()-interval '61 seconds'`); e != nil {
			t.Fatal(e)
		}
	}
	receive := func() string {
		if e := a.deliverContactVerification(ctx); e != nil {
			t.Fatal(e)
		}
		select {
		case body := <-messages:
			match := regexp.MustCompile(`verification code: ([0-9]{6})`).FindStringSubmatch(body)
			if len(match) != 2 {
				t.Fatal("verification message missing")
			}
			return match[1]
		case <-time.After(time.Second):
			t.Fatal("no verification delivery")
		}
		return ""
	}
	start := func(kind, address string) string {
		reset()
		r := call(t, srv.URL, "POST", "/api/account/contacts/start", map[string]string{"kind": kind, "address": address, "password": testPassword}, owner, origin)
		expect(t, r, 200)
		return object(t, r)["challenge"].(string)
	}
	challenge := start("email", "owner@example.test")
	code := receive()
	var cipher []byte
	var digest string
	if e := pool.QueryRow(ctx, `SELECT code_hash FROM contact_challenges WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`, uid).Scan(&digest); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(digest, code) {
		t.Fatal("plaintext code")
	}
	pool.QueryRow(ctx, `SELECT payload_sealed FROM contact_deliveries ORDER BY next_attempt DESC LIMIT 1`).Scan(&cipher)
	if len(cipher) != 0 {
		t.Fatal("delivered code retained")
	}
	finish := map[string]string{"challenge": challenge, "code": code}
	expect(t, call(t, srv.URL, "POST", "/api/account/contacts/finish", finish, other, origin), 400)
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	expect(t, call(t, srv.URL, "POST", "/api/account/contacts/finish", map[string]string{"challenge": challenge, "code": wrong}, owner, origin), 400)
	expect(t, call(t, srv.URL, "POST", "/api/account/contacts/finish", finish, owner, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/account/contacts/finish", finish, owner, origin), 400)
	challenge = start("phone", "+8613800000000")
	code = receive()
	expect(t, call(t, srv.URL, "POST", "/api/account/contacts/finish", map[string]string{"challenge": challenge, "code": code}, owner, origin), 200)
	publicStart := func(purpose, kind, address string) result {
		reset()
		r := call(t, srv.URL, "POST", "/api/contact/start", map[string]string{"purpose": purpose, "kind": kind, "address": address}, nil, secure)
		expect(t, r, 200)
		if r.cookie == nil || !r.cookie.Secure || !r.cookie.HttpOnly {
			t.Fatal("contact browser binding")
		}
		return r
	}
	r := publicStart("sms-login", "phone", "+8613800000000")
	code = receive()
	body := map[string]string{"purpose": "sms-login", "challenge": object(t, r)["challenge"].(string), "code": code}
	expect(t, call(t, srv.URL, "POST", "/api/contact/finish", body, nil, secure), 400)
	logged := call(t, srv.URL, "POST", "/api/contact/finish", body, r.cookie, secure)
	assertCookieDuration(t, logged.cookie, 7*60)
	expect(t, logged, 200)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, logged.cookie, origin), 200)
	_, _, owner = enroll(t, srv, owner)
	r = publicStart("sms-login", "phone", "+8613800000000")
	code = receive()
	body = map[string]string{"purpose": "sms-login", "challenge": object(t, r)["challenge"].(string), "code": code}
	pending := call(t, srv.URL, "POST", "/api/contact/finish", body, r.cookie, secure)
	expect(t, pending, 200)
	if object(t, pending)["needTotp"] != true {
		t.Fatal("MFA bypass")
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, pending.cookie, origin), 401)
	r = publicStart("recovery", "email", "owner@example.test")
	code = receive()
	body = map[string]string{"purpose": "recovery", "challenge": object(t, r)["challenge"].(string), "code": code, "newPassword": "new recovery password 2026"}
	expect(t, call(t, srv.URL, "POST", "/api/contact/finish", body, r.cookie, secure), 200)
	expect(t, call(t, srv.URL, "POST", "/api/contact/finish", body, r.cookie, secure), 400)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, owner, origin), 401)
	expect(t, call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "contact-user", "password": testPassword}, nil, origin), 400)
	restored := call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "contact-user", "password": "new recovery password 2026"}, nil, origin)
	expect(t, restored, 200)
	if object(t, restored)["needTotp"] != true {
		t.Fatal("password recovery removed MFA")
	}
	r = publicStart("recovery", "email", "unknown@example.test")
	if len(object(t, r)["challenge"].(string)) != 64 {
		t.Fatal("enumeration response")
	}
	if e := a.deliverContactVerification(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-messages:
		t.Fatal("unknown address got message")
	default:
	}
	r = publicStart("recovery", "email", "owner@example.test")
	code = receive()
	body = map[string]string{"purpose": "recovery", "challenge": object(t, r)["challenge"].(string), "code": wrong, "newPassword": "another safe password 2026"}
	if wrong == code {
		body["code"] = "999999"
	}
	for i := 0; i < 5; i++ {
		expect(t, call(t, srv.URL, "POST", "/api/contact/finish", body, r.cookie, secure), 400)
	}
	body["code"] = code
	expect(t, call(t, srv.URL, "POST", "/api/contact/finish", body, r.cookie, secure), 400)
}
