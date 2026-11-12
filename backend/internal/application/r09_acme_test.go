package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"next-terminal/backend/internal/modules/pki"
	"os"
	"testing"
)

func TestR09InternalACMEIssueRenewAndRetainOnFailure(t *testing.T) {
	if os.Getenv("NT_TEST_ACME_FIXTURE") != "true" {
		t.Skip("requires internal RFC2136 and Pebble fixture profile")
	}
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ctx := context.Background()
	data, e := os.ReadFile("../../.local/r09-fixtures/config.json")
	if e != nil {
		t.Fatal(e)
	}
	var conf struct {
		Secret    string `json:"secret"`
		DNSSecret string `json:"dnsSecret"`
	}
	if e = json.Unmarshal(data, &conf); e != nil {
		t.Fatal(e)
	}
	ca, e := os.ReadFile("../../.local/r09-fixtures/cert.pem")
	if e != nil {
		t.Fatal(e)
	}
	control := func(fail bool) {
		t.Helper()
		payload, _ := json.Marshal(map[string]bool{"DNS": fail})
		req, _ := http.NewRequest("POST", "http://fixture-services:8080/control", bytes.NewReader(payload))
		req.Header.Set("X-Fixture-Control", conf.Secret)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != 204 {
			t.Fatal(res.StatusCode)
		}
	}
	control(false)
	defer control(false)
	account := postID(t, srv, "/api/admin/acme/accounts", pki.Account{Name: "Pebble internal", DirectoryURL: "https://acme-fixture:14000/dir", Email: "acceptance@example.test", CA: string(ca), TermsAccepted: true}, admin)
	provider := postID(t, srv, "/api/admin/acme/providers", pki.Provider{Name: "RFC2136 internal", Zone: "localhost.", Server: "fixture-services:8053", KeyName: "fixture-key.", Secret: conf.DNSSecret}, admin)
	probe := pki.Provider{Zone: "localhost.", Server: "fixture-services:8053", KeyName: "fixture-key.", Secret: conf.DNSSecret}
	if e = pki.DNSUpdate(ctx, probe, "_acme-challenge.probe.localhost.", "test-probe", false); e != nil {
		t.Fatal("DNS protocol probe", e)
	}
	if e = pki.DNSUpdate(ctx, probe, "_acme-challenge.probe.localhost.", "test-probe", true); e != nil {
		t.Fatal("DNS cleanup probe", e)
	}
	id := postID(t, srv, "/api/admin/acme/orders", pki.Order{Name: "internal website cert", AccountID: account, ProviderID: provider, Domains: []string{"r09-acme.localhost"}, AutoRenew: true, RenewBefore: 30}, admin)
	if e = a.certificates().ProcessOrder(ctx); e != nil {
		t.Fatal(e)
	}
	orders, e := a.certificates().Orders(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if len(orders) != 1 || orders[0].Status != "issued" {
		t.Fatalf("ACME issue: %+v", orders)
	}
	cert, e := a.certificates().Get(ctx, orders[0].CertificateID)
	if e != nil {
		t.Fatal(e)
	}
	pair, e := a.certificates().TLS(ctx, cert.ID)
	if e != nil || pair.Leaf.VerifyHostname("r09-acme.localhost") != nil {
		t.Fatal("issued certificate invalid", e)
	}
	var residual int
	if e = a.db.Row(ctx, "SELECT count(*) FROM acme_dns_records WHERE order_id=$1", id).Scan(&residual); e != nil || residual != 0 {
		t.Fatal("DNS cleanup", residual, e)
	}
	control(true)
	if _, e = a.db.Exec(ctx, "UPDATE certificate_orders SET next_attempt=now() WHERE id=$1", id); e != nil {
		t.Fatal(e)
	}
	if e = a.certificates().ProcessOrder(ctx); e != nil {
		t.Fatal(e)
	}
	orders, _ = a.certificates().Orders(ctx)
	if orders[0].Status != "failed" {
		t.Fatalf("DNS failure not recorded %+v", orders)
	}
	old, e := a.certificates().Get(ctx, cert.ID)
	if e != nil || old.Fingerprint != cert.Fingerprint {
		t.Fatal("failed renewal replaced working cert", e)
	}
	control(false)
	expect(t, call(t, srv.URL, "POST", "/api/admin/acme/orders/"+id+"/retry", map[string]int64{"version": orders[0].Version}, admin, origin), 202)
	if e = a.certificates().ProcessOrder(ctx); e != nil {
		t.Fatal(e)
	}
	orders, _ = a.certificates().Orders(ctx)
	renewed, e := a.certificates().Get(ctx, cert.ID)
	if e != nil || orders[0].Status != "issued" || renewed.Fingerprint == cert.Fingerprint || renewed.Version != cert.Version+1 {
		t.Fatalf("renewal failed: %+v %v", orders, e)
	}
}
