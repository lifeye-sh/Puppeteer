package pki

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/miekg/dns"
	"golang.org/x/crypto/acme"
	"net/http"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/secretbox"
	"slices"
	"strings"
	"time"
)

func (s *Service) acmeClient(ctx context.Context, id string) (*acme.Client, error) {
	var directory, email, ca, kid string
	var encrypted []byte
	if e := s.db.Row(ctx, "SELECT directory_url,email,ca_pem,key_sealed,account_url FROM acme_accounts WHERE id=$1", id).Scan(&directory, &email, &ca, &encrypted, &kid); e != nil {
		return nil, e
	}
	der, e := secretbox.Open(s.key, encrypted, "acme:"+id)
	if e != nil {
		return nil, e
	}
	defer clear(der)
	key, e := x509.ParsePKCS8PrivateKey(der)
	if e != nil {
		return nil, e
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fault.Invalid
	}
	roots, e := x509.SystemCertPool()
	if e != nil {
		roots = x509.NewCertPool()
	}
	if ca != "" && !roots.AppendCertsFromPEM([]byte(ca)) {
		return nil, fault.Invalid
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DisableKeepAlives: true}
	client := &acme.Client{Key: signer, DirectoryURL: directory, KID: acme.KeyID(kid), HTTPClient: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if kid == "" {
		account, e := client.Register(ctx, &acme.Account{Contact: []string{"mailto:" + email}}, func(string) bool { return true })
		if e != nil {
			return nil, e
		}
		if _, e = s.db.Exec(ctx, "UPDATE acme_accounts SET account_url=$2 WHERE id=$1", id, account.URI); e != nil {
			return nil, e
		}
		client.KID = acme.KeyID(account.URI)
	}
	return client, nil
}
func (s *Service) dnsProvider(ctx context.Context, id string) (v Provider, e error) {
	var encrypted []byte
	e = s.db.Row(ctx, "SELECT id::text,name,zone,server,key_name,secret_sealed FROM dns_providers WHERE id=$1", id).Scan(&v.ID, &v.Name, &v.Zone, &v.Server, &v.KeyName, &encrypted)
	if e != nil {
		return
	}
	secret, e := secretbox.Open(s.key, encrypted, "dns:"+id)
	if e != nil {
		return v, e
	}
	defer clear(secret)
	v.Secret = string(secret)
	return
}
func DNSUpdate(ctx context.Context, p Provider, name, value string, remove bool) error {
	if !dns.IsSubDomain(p.Zone, dns.Fqdn(name)) {
		return fault.Denied
	}
	rr := &dns.TXT{Hdr: dns.RR_Header{Name: dns.Fqdn(name), Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 60}, Txt: []string{value}}
	m := new(dns.Msg)
	m.SetUpdate(p.Zone)
	if remove {
		m.Remove([]dns.RR{rr})
	} else {
		m.Insert([]dns.RR{rr})
	}
	m.SetTsig(p.KeyName, dns.HmacSHA256, 300, time.Now().Unix())
	client := &dns.Client{Net: "tcp", Timeout: 10 * time.Second, TsigSecret: map[string]string{p.KeyName: p.Secret}}
	res, _, e := client.ExchangeContext(ctx, m, p.Server)
	if e != nil {
		return e
	}
	if res.Rcode != dns.RcodeSuccess {
		return errors.New("DNS update rejected")
	}
	return nil
}
func DNSVisible(ctx context.Context, p Provider, name, value string) error {
	client := &dns.Client{Net: "tcp", Timeout: 5 * time.Second}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn(name), dns.TypeTXT)
		res, _, e := client.ExchangeContext(ctx, m, p.Server)
		if e == nil && res.Rcode == dns.RcodeSuccess {
			for _, rr := range res.Answer {
				if txt, ok := rr.(*dns.TXT); ok && strings.Join(txt.Txt, "") == value {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (s *Service) CleanupDNS(ctx context.Context, order string, p Provider) error {
	rows, e := s.db.Rows(ctx, "SELECT name,value FROM acme_dns_records WHERE order_id=$1", order)
	if e != nil {
		return e
	}
	type record struct{ name, value string }
	records := []record{}
	for rows.Next() {
		var v record
		if e = rows.Scan(&v.name, &v.value); e != nil {
			rows.Close()
			return e
		}
		records = append(records, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range records {
		if e = DNSUpdate(ctx, p, v.name, v.value, true); e != nil {
			return e
		}
		if _, e = s.db.Exec(ctx, "DELETE FROM acme_dns_records WHERE order_id=$1 AND name=$2 AND value=$3", order, v.name, v.value); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) issueACME(ctx context.Context, v Order) (cert, key []byte, stage string, err error) {
	stage = "dns_configuration"
	provider, e := s.dnsProvider(ctx, v.ProviderID)
	if e != nil {
		return nil, nil, stage, e
	}
	if e = s.CleanupDNS(ctx, v.ID, provider); e != nil {
		return nil, nil, "dns_cleanup", e
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if e := s.CleanupDNS(cleanup, v.ID, provider); e != nil {
			_, _ = s.db.Exec(cleanup, "INSERT INTO certificate_order_events(order_id,status,message) VALUES($1,'warning','DNS cleanup pending; will retry before next attempt')", v.ID)
		}
	}()
	stage = "account_registration"
	client, e := s.acmeClient(ctx, v.AccountID)
	if e != nil {
		return nil, nil, stage, e
	}
	identifiers := make([]acme.AuthzID, len(v.Domains))
	for i, domain := range v.Domains {
		identifiers[i] = acme.AuthzID{Type: "dns", Value: domain}
	}
	stage = "order_creation"
	order, e := client.AuthorizeOrder(ctx, identifiers)
	if e != nil {
		return nil, nil, stage, e
	}
	for _, authURL := range order.AuthzURLs {
		stage = "authorization"
		auth, e := client.GetAuthorization(ctx, authURL)
		if e != nil {
			return nil, nil, stage, e
		}
		if auth.Status == acme.StatusValid {
			continue
		}
		var challenge *acme.Challenge
		for _, ch := range auth.Challenges {
			if ch.Type == "dns-01" {
				challenge = ch
				break
			}
		}
		if challenge == nil {
			return nil, nil, stage, errors.New("DNS01 unavailable")
		}
		value, e := client.DNS01ChallengeRecord(challenge.Token)
		if e != nil {
			return nil, nil, stage, e
		}
		name := "_acme-challenge." + strings.TrimPrefix(auth.Identifier.Value, "*.") + "."
		stage = "dns_present"
		if _, e = s.db.Exec(ctx, "INSERT INTO acme_dns_records(order_id,name,value) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", v.ID, name, value); e != nil {
			return nil, nil, stage, e
		}
		if e = DNSUpdate(ctx, provider, name, value, false); e != nil {
			return nil, nil, stage, e
		}
		stage = "dns_propagation"
		if e = DNSVisible(ctx, provider, name, value); e != nil {
			return nil, nil, stage, e
		}
		stage = "challenge_validation"
		if _, e = client.Accept(ctx, challenge); e != nil {
			return nil, nil, stage, e
		}
		if _, e = client.WaitAuthorization(ctx, authURL); e != nil {
			return nil, nil, stage, e
		}
	}
	stage = "order_ready"
	order, e = client.WaitOrder(ctx, order.URI)
	if e != nil {
		return nil, nil, stage, e
	}
	private, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, nil, stage, e
	}
	csr, e := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: v.Domains[0]}, DNSNames: v.Domains}, private)
	if e != nil {
		return nil, nil, stage, e
	}
	stage = "certificate_issuance"
	chain, _, e := client.CreateCertFromOrder(ctx, order, csr, true)
	if e != nil {
		return nil, nil, stage, e
	}
	if len(chain) == 0 {
		return nil, nil, stage, fault.Invalid
	}
	leaf, e := x509.ParseCertificate(chain[0])
	if e != nil {
		return nil, nil, stage, e
	}
	for _, domain := range v.Domains {
		if !slices.Contains(leaf.DNSNames, domain) {
			return nil, nil, "certificate_domain_validation", fault.Invalid
		}
	}
	for _, der := range chain {
		cert = append(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	der, e := x509.MarshalPKCS8PrivateKey(private)
	if e != nil {
		return nil, nil, stage, e
	}
	defer clear(der)
	key = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return cert, key, "issued", nil
}

// RecoverDNS removes only our recorded TXT values while holding the order row lock.
// The issuance worker skips that row, so cleanup cannot race a new challenge.
func (s *Service) RecoverDNS(ctx context.Context) error {
	return s.db.Within(ctx, func(c context.Context) error {
		var id, providerID string
		e := s.db.Row(c, `SELECT o.id::text,o.provider_id::text FROM certificate_orders o WHERE o.status<>'processing' AND EXISTS(SELECT 1 FROM acme_dns_records d WHERE d.order_id=o.id) ORDER BY o.next_attempt LIMIT 1 FOR UPDATE OF o SKIP LOCKED`).Scan(&id, &providerID)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		provider, e := s.dnsProvider(c, providerID)
		if e != nil {
			return e
		}
		return s.CleanupDNS(c, id, provider)
	})
}
func (s *Service) ProcessOrder(ctx context.Context) error {
	var id, lease string
	e := s.db.Row(ctx, `WITH candidate AS (SELECT o.id FROM certificate_orders o LEFT JOIN certificates c ON c.id=o.certificate_id WHERE ((o.status IN ('pending','failed') AND o.next_attempt<=now()) OR (o.status='processing' AND o.lease_until<now()) OR (o.status='issued' AND o.auto_renew AND c.revoked_at IS NULL AND c.not_after<now()+make_interval(days=>o.renew_before_days) AND o.next_attempt<=now())) ORDER BY o.next_attempt LIMIT 1 FOR UPDATE OF o SKIP LOCKED) UPDATE certificate_orders o SET status='processing',attempts=attempts+1,lease_token=gen_random_uuid()::text,lease_until=now()+interval '6 minutes' FROM candidate c WHERE o.id=c.id RETURNING o.id::text,o.lease_token`).Scan(&id, &lease)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	v, e := scanOrder(s.db.Row(ctx, "SELECT "+orderColumns+" FROM certificate_orders WHERE id=$1", id))
	if e != nil {
		return e
	}
	work, cancel := context.WithTimeout(ctx, 4*time.Minute)
	cert, key, stage, issueErr := s.issueACME(work, v)
	cancel()
	defer clear(key)
	finish, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	return s.db.Within(finish, func(c context.Context) error {
		var current string
		if e := s.db.Row(c, "SELECT lease_token FROM certificate_orders WHERE id=$1 FOR UPDATE", id).Scan(&current); e != nil {
			return e
		}
		if current != lease {
			return fault.Conflict
		}
		if issueErr != nil {
			_, e := s.db.Exec(c, `UPDATE certificate_orders SET status='failed',last_error=$2,next_attempt=now()+interval '15 minutes',lease_until=NULL,lease_token='',version=version+1 WHERE id=$1`, id, stage+" failed; verify ACME/DNS configuration and connectivity")
			if e != nil {
				return e
			}
			_, e = s.db.Exec(c, "INSERT INTO certificate_order_events(order_id,status,message) VALUES($1,'failed',$2)", id, stage)
			return e
		}
		entry := Certificate{ID: v.CertificateID, Name: v.Name, Kind: "server", Source: "acme", PEM: string(cert)}
		if entry.ID != "" {
			existing, e := s.Get(c, entry.ID)
			if e != nil {
				return e
			}
			entry.Version = existing.Version
		}
		certID, e := s.Store(c, entry, key)
		if e != nil {
			return e
		}
		if _, e = s.db.Exec(c, `UPDATE certificate_orders SET status='issued',certificate_id=$2,last_error='',next_attempt=now()+interval '1 hour',lease_until=NULL,lease_token='',version=version+1 WHERE id=$1`, id, certID); e != nil {
			return e
		}
		_, e = s.db.Exec(c, "INSERT INTO certificate_order_events(order_id,status,message) VALUES($1,'issued','Certificate stored; previous certificate retained on failed renewal')", id)
		return e
	})
}
