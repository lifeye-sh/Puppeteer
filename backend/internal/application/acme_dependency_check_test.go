package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"puppeteer/backend/internal/modules/pki"
)

func TestACMEDependencyDoesNotIssueOrChangeDNS(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ctx := context.Background()
	var calls atomic.Int32
	var base string
	vendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/directory" {
			t.Errorf("unexpected ACME side effect %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"newNonce": base + "/nonce", "newAccount": base + "/account", "newOrder": base + "/order"})
	}))
	defer vendor.Close()
	base = vendor.URL
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: vendor.TLS.Certificates[0].Certificate[0]}))
	account := postID(t, srv, "/api/admin/acme/accounts", pki.Account{Name: "readonly account", DirectoryURL: base + "/directory", Email: "test@example.test", CA: ca, TermsAccepted: true}, admin)
	secret := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var queries atomic.Int32
	dnsServer := &dns.Server{Listener: listener, Net: "tcp", TsigSecret: map[string]string{"fixture.": secret}, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		queries.Add(1)
		if r.Opcode != dns.OpcodeQuery || len(r.Question) != 1 || r.Question[0].Qtype != dns.TypeSOA {
			t.Error("DNS mutation attempted")
		}
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		m.Answer = []dns.RR{&dns.SOA{Hdr: dns.RR_Header{Name: "example.test.", Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 60}, Ns: "ns.example.test.", Mbox: "hostmaster.example.test.", Serial: 1, Refresh: 60, Retry: 60, Expire: 60, Minttl: 60}}
		m.SetTsig("fixture.", dns.HmacSHA256, 300, time.Now().Unix())
		if e := w.WriteMsg(m); e != nil {
			t.Error(e)
		}
	})}
	go dnsServer.ActivateAndServe()
	defer dnsServer.Shutdown()
	provider := postID(t, srv, "/api/admin/acme/providers", pki.Provider{Name: "readonly DNS", Zone: "example.test.", Server: listener.Addr().String(), KeyName: "fixture.", Secret: secret}, admin)
	order := postID(t, srv, "/api/admin/acme/orders", pki.Order{Name: "readonly order", AccountID: account, ProviderID: provider, Domains: []string{"node.example.test"}, RenewBefore: 30}, admin)
	feature := "acme-order:" + order
	snapshot := object(t, call(t, srv.URL, "GET", "/api/settings/operations/"+feature, nil, admin, origin))
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	r, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/"+feature+"/preflight", tag, map[string]any{"evidence": snapshot["evidence"]}, admin)
	expect(t, r, 202)
	if task := configurationTaskResult(t, srv, admin, object(t, r)["id"].(string)); task.State != "succeeded" {
		t.Fatal(task)
	}
	if calls.Load() != 1 || queries.Load() != 1 {
		t.Fatal("unexpected external operations", calls.Load(), queries.Load())
	}
	var status, kid string
	var records int
	if err = a.db.Row(ctx, `SELECT o.status,a.account_url,(SELECT count(*) FROM acme_dns_records WHERE order_id=o.id) FROM certificate_orders o JOIN acme_accounts a ON a.id=o.account_id WHERE o.id=$1`, order).Scan(&status, &kid, &records); err != nil || status != "pending" || kid != "" || records != 0 {
		t.Fatal("preflight mutated order", status, kid, records, err)
	}
	r, _ = settingsRequest(t, srv.URL, "POST", "/api/settings/operations/"+feature+"/verify", tag, map[string]any{"evidence": snapshot["evidence"]}, admin)
	expect(t, r, 422)
}
