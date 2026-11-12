package application

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/modules/website"
	"testing"
)

func TestR16ClientIPConfigurationAndLegacyAdapter(t *testing.T) {
	_, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	save := func(values map[string]any, want int) {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": values}, admin)
		expect(t, r, want)
	}
	resolve := func(remote, header string, values []string, want string) {
		t.Helper()
		r := httptest.NewRequest("GET", "https://console.test/api/account/info", nil)
		r.RemoteAddr = remote
		r.Header[header] = values
		resolved, e := a.resolveAPIClient(r)
		if e != nil {
			t.Fatal(e)
		}
		if got := sourceIP(resolved); got != want {
			t.Fatalf("source IP got %q want %q", got, want)
		}
	}
	resolve("127.0.0.1:1234", "X-Forwarded-For", []string{"203.0.113.9"}, "127.0.0.1")
	for _, values := range []map[string]any{{"ip-extractor": "cookie", "ip-trust-list": "127.0.0.1/32"}, {"ip-extractor": "x-forwarded-for"}, {"ip-trust-list": "not-cidr"}, {"ip-extractor": "X-Real-IP"}, {"ip-trust-list": "::ffff:127.0.0.1/128"}} {
		save(values, 422)
	}
	save(map[string]any{"ip-extractor": "x-forwarded-for", "ip-trust-list": "127.0.0.1/32,10.0.0.0/8"}, 202)
	resolve("127.0.0.1:1234", "X-Forwarded-For", []string{"203.0.113.9, 10.2.0.3"}, "203.0.113.9")
	resolve("198.51.100.5:1234", "X-Forwarded-For", []string{"203.0.113.9"}, "198.51.100.5")
	resolve("127.0.0.1:1234", "X-Forwarded-For", []string{"203.0.113.9, 198.51.100.5"}, "198.51.100.5")
	resolve("127.0.0.1:1234", "X-Forwarded-For", []string{"203.0.113.9", "198.51.100.5"}, "127.0.0.1")
	resolve("127.0.0.1:1234", "X-Forwarded-For", []string{"fe80::1%untrusted"}, "127.0.0.1")
	save(map[string]any{"ip-extractor": "x-client-ip"}, 202)
	resolve("127.0.0.1:1234", "X-Client-Ip", []string{"203.0.113.10"}, "203.0.113.10")
	resolve("127.0.0.1:1234", "X-Client-Ip", []string{"203.0.113.10,10.0.0.1"}, "127.0.0.1")
	// Website settings keep an optimistic version and mirror desired API trust.
	r := call(t, srv.URL, "GET", "/api/admin/website-settings", nil, admin, origin)
	expect(t, r, 200)
	var legacy website.Settings
	if e := json.Unmarshal(r.body, &legacy); e != nil {
		t.Fatal(e)
	}
	old := legacy
	legacy.APITrustedProxies = []string{"127.0.0.1/32"}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/website-settings", legacy, admin, origin), 204)
	v, e := a.system.Configuration(ctx, 0)
	if e != nil || v.Values.String("ip-trust-list") != "127.0.0.1/32" || v.Values.String("ip-extractor") != "x-client-ip" {
		t.Fatal("legacy adapter did not preserve extractor", e)
	}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/website-settings", old, admin, origin), 409)
	// Trust expansion waits for node ACK; source attribution keeps the old set.
	for _, node := range []string{"ip-a", "ip-b"} {
		if _, e = a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	save(map[string]any{"ip-trust-list": "127.0.0.1/32,10.0.0.0/8"}, 202)
	if _, e = a.system.ApplyConfiguration(ctx, "ip-a", func(system.Revision) error { return nil }); e != nil {
		t.Fatal(e)
	}
	resolve("10.2.0.3:1234", "X-Client-Ip", []string{"203.0.113.10"}, "10.2.0.3")
	if _, e = a.system.ApplyConfiguration(ctx, "ip-b", func(system.Revision) error { return nil }); e != nil {
		t.Fatal(e)
	}
	resolve("10.2.0.3:1234", "X-Client-Ip", []string{"203.0.113.10"}, "203.0.113.10")
	save(map[string]any{"ip-extractor": "direct"}, 202)
	resolve("10.2.0.3:1234", "X-Client-Ip", []string{"203.0.113.10"}, "10.2.0.3")
}
