package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"next-terminal/backend/internal/platform/secretbox"
	"regexp"
	"time"
)

var smsAddress = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

// SMS webhook contract: TLS + Bearer secret, JSON id/to/text; 200 {"accepted":true}.
func sendSMS(ctx context.Context, c Config, secret, to, id, body string) error {
	if secret == "" || !smsAddress.MatchString(to) {
		return errTransport
	}
	u, e := url.Parse(c.APIBase)
	if e != nil || u.Scheme != "https" || u.User != nil {
		return errTransport
	}
	tc, e := transportTLS(c)
	if e != nil {
		return e
	}
	tc.ServerName = u.Hostname()
	transport := &http.Transport{TLSClientConfig: tc}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	data, _ := json.Marshal(map[string]string{"id": id, "to": to, "text": body})
	req, e := http.NewRequestWithContext(ctx, "POST", c.APIBase, bytes.NewReader(data))
	if e != nil {
		return errTransport
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", id)
	res, e := client.Do(req)
	if e != nil {
		return errTransport
	}
	defer res.Body.Close()
	var out struct {
		Accepted bool `json:"accepted"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&out) != nil || !out.Accepted {
		return errTransport
	}
	return nil
}
func (s *Service) SendVerification(ctx context.Context, channel string, version int64, address, id, body string) error {
	var kind, aad string
	var config, sealed []byte
	if e := s.db.Row(ctx, `SELECT c.type,c.config,coalesce(s.sealed,c.secret),coalesce('settings:'||s.key||':'||s.id::text,'notification:'||c.id::text) FROM notification_channels c LEFT JOIN configuration_secrets s ON s.id=c.configuration_secret_id WHERE c.id=$1 AND c.version=$2 AND c.enabled`, channel, version).Scan(&kind, &config, &sealed, &aad); e != nil {
		return errTransport
	}
	var c Config
	if json.Unmarshal(config, &c) != nil {
		return errTransport
	}
	var secret []byte
	var e error
	if len(sealed) > 0 {
		secret, e = secretbox.Open(s.key, sealed, aad)
		if e != nil {
			return errTransport
		}
		defer clear(secret)
	}
	switch kind {
	case "smtp":
		return sendSMTP(ctx, c, string(secret), address, id, "Puppeteer verification", body)
	case "sms-webhook":
		return sendSMS(ctx, c, string(secret), address, id, body)
	}
	return errTransport
}
