package notification

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSMSWebhookTransportBoundaries(t *testing.T) {
	for _, scenario := range []string{"success", "refused", "redirect", "bad-json", "oversized", "untrusted", "http", "invalid-recipient", "empty-secret"} {
		t.Run(scenario, func(t *testing.T) {
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-secret" || r.Header.Get("Idempotency-Key") != "delivery-id" {
					t.Error("missing delivery authorization")
				}
				var p map[string]string
				if json.NewDecoder(r.Body).Decode(&p) != nil || p["id"] != "delivery-id" || p["to"] != "+8613800000000" || p["text"] != "fixture message" {
					t.Error("unexpected delivery")
				}
				switch scenario {
				case "refused":
					w.Write([]byte(`{"accepted":false}`))
				case "redirect":
					http.Redirect(w, r, "https://attacker.invalid", 302)
				case "bad-json":
					w.Write([]byte(`not JSON`))
				case "oversized":
					w.Write([]byte(strings.Repeat(" ", 65537) + `{"accepted":true}`))
				default:
					w.Write([]byte(`{"accepted":true}`))
				}
			}))
			defer target.Close()
			c := Config{APIBase: target.URL, CA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: target.Certificate().Raw}))}
			recipient, secret := "+8613800000000", "fixture-secret"
			if scenario == "untrusted" {
				c.CA = ""
			}
			if scenario == "http" {
				c.APIBase = strings.Replace(c.APIBase, "https:", "http:", 1)
			}
			if scenario == "invalid-recipient" {
				recipient = "13800000000\r\n"
			}
			if scenario == "empty-secret" {
				secret = ""
			}
			e := sendSMS(context.Background(), c, secret, recipient, "delivery-id", "fixture message")
			if (e == nil) != (scenario == "success") {
				t.Fatalf("transport result %v", e)
			}
		})
	}
}
