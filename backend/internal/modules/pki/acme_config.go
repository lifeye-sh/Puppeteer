package pki

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"github.com/miekg/dns"
	"net"
	"net/mail"
	"net/url"
	"next-terminal/backend/internal/modules/website"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/secretbox"
	"strings"
	"time"
)

type Account struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DirectoryURL  string `json:"directoryUrl"`
	Email         string `json:"email"`
	CA            string `json:"ca"`
	Version       int64  `json:"version"`
	TermsAccepted bool   `json:"termsAccepted"`
}
type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Zone    string `json:"zone"`
	Server  string `json:"server"`
	KeyName string `json:"keyName"`
	Secret  string `json:"secret,omitempty"`
	Version int64  `json:"version"`
}
type Order struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	AccountID     string    `json:"accountId"`
	ProviderID    string    `json:"providerId"`
	Domains       []string  `json:"domains"`
	CertificateID string    `json:"certificateId"`
	Status        string    `json:"status"`
	AutoRenew     bool      `json:"autoRenew"`
	RenewBefore   int       `json:"renewBeforeDays"`
	NextAttempt   time.Time `json:"nextAttempt"`
	Attempts      int       `json:"attempts"`
	Error         string    `json:"error"`
	Version       int64     `json:"version"`
}

func (s *Service) Accounts(ctx context.Context) ([]Account, error) {
	rows, e := s.db.Rows(ctx, "SELECT id::text,name,directory_url,email,ca_pem,version FROM acme_accounts ORDER BY name,id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var v Account
		if e = rows.Scan(&v.ID, &v.Name, &v.DirectoryURL, &v.Email, &v.CA, &v.Version); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) SaveAccount(ctx context.Context, v Account) (string, error) {
	u, e := url.Parse(v.DirectoryURL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || !v.TermsAccepted || v.Name == "" || len(v.Name) > 100 || len(v.CA) > 65536 {
		return "", fault.Invalid
	}
	if _, e = mail.ParseAddress(v.Email); e != nil {
		return "", fault.Invalid
	}
	if v.ID != "" {
		tag, e := s.db.Exec(ctx, "UPDATE acme_accounts SET name=$2,email=$3,ca_pem=$4,version=version+1 WHERE id=$1 AND version=$5 AND directory_url=$6", v.ID, v.Name, v.Email, v.CA, v.Version, v.DirectoryURL)
		if e == nil && tag.RowsAffected() != 1 {
			e = fault.Conflict
		}
		return v.ID, e
	}
	if e = s.db.Row(ctx, "SELECT gen_random_uuid()::text").Scan(&v.ID); e != nil {
		return "", e
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return "", e
	}
	der, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		return "", e
	}
	defer clear(der)
	encrypted, e := secretbox.Seal(s.key, der, "acme:"+v.ID)
	if e != nil {
		return "", e
	}
	_, e = s.db.Exec(ctx, "INSERT INTO acme_accounts(id,name,directory_url,email,ca_pem,key_sealed) VALUES($1,$2,$3,$4,$5,$6)", v.ID, v.Name, v.DirectoryURL, v.Email, v.CA, encrypted)
	return v.ID, e
}
func (s *Service) Providers(ctx context.Context) ([]Provider, error) {
	rows, e := s.db.Rows(ctx, "SELECT id::text,name,zone,server,key_name,version FROM dns_providers ORDER BY name,id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Provider{}
	for rows.Next() {
		var v Provider
		if e = rows.Scan(&v.ID, &v.Name, &v.Zone, &v.Server, &v.KeyName, &v.Version); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) SaveProvider(ctx context.Context, v Provider) (string, error) {
	v.Zone = dns.Fqdn(strings.ToLower(v.Zone))
	v.KeyName = dns.Fqdn(v.KeyName)
	if _, ok := dns.IsDomainName(v.Zone); !ok {
		return "", fault.Invalid
	}
	if _, ok := dns.IsDomainName(v.KeyName); !ok {
		return "", fault.Invalid
	}
	if _, _, e := net.SplitHostPort(v.Server); e != nil {
		return "", fault.Invalid
	}
	if v.Name == "" || len(v.Name) > 100 || len(v.Secret) > 4096 {
		return "", fault.Invalid
	}
	if v.Secret != "" {
		key, e := base64.StdEncoding.DecodeString(v.Secret)
		if e != nil || len(key) < 32 {
			return "", fault.Invalid
		}
	}
	if v.ID == "" {
		if v.Secret == "" {
			return "", fault.Invalid
		}
		if e := s.db.Row(ctx, "SELECT gen_random_uuid()::text").Scan(&v.ID); e != nil {
			return "", e
		}
		encrypted, e := secretbox.Seal(s.key, []byte(v.Secret), "dns:"+v.ID)
		if e != nil {
			return "", e
		}
		_, e = s.db.Exec(ctx, "INSERT INTO dns_providers(id,name,zone,server,key_name,secret_sealed) VALUES($1,$2,$3,$4,$5,$6)", v.ID, v.Name, v.Zone, v.Server, v.KeyName, encrypted)
		return v.ID, e
	}
	tag, e := s.db.Exec(ctx, "UPDATE dns_providers SET name=$2,zone=$3,server=$4,key_name=$5,version=version+1 WHERE id=$1 AND version=$6", v.ID, v.Name, v.Zone, v.Server, v.KeyName, v.Version)
	if e != nil {
		return "", e
	}
	if tag.RowsAffected() != 1 {
		return "", fault.Conflict
	}
	if v.Secret != "" {
		encrypted, e := secretbox.Seal(s.key, []byte(v.Secret), "dns:"+v.ID)
		if e != nil {
			return "", e
		}
		_, e = s.db.Exec(ctx, "UPDATE dns_providers SET secret_sealed=$2 WHERE id=$1", v.ID, encrypted)
		if e != nil {
			return "", e
		}
	}
	return v.ID, nil
}

const orderColumns = `id::text,name,account_id::text,provider_id::text,domains,coalesce(certificate_id::text,''),status,auto_renew,renew_before_days,next_attempt,attempts,last_error,version`

func scanOrder(row scanner) (v Order, e error) {
	e = row.Scan(&v.ID, &v.Name, &v.AccountID, &v.ProviderID, &v.Domains, &v.CertificateID, &v.Status, &v.AutoRenew, &v.RenewBefore, &v.NextAttempt, &v.Attempts, &v.Error, &v.Version)
	return
}
func (s *Service) Orders(ctx context.Context) ([]Order, error) {
	rows, e := s.db.Rows(ctx, "SELECT "+orderColumns+" FROM certificate_orders ORDER BY created_at DESC")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Order{}
	for rows.Next() {
		v, e := scanOrder(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) SaveOrder(ctx context.Context, v Order) (string, error) {
	if v.Name == "" || len(v.Name) > 100 || len(v.Domains) == 0 || len(v.Domains) > 20 || v.RenewBefore < 1 || v.RenewBefore > 60 {
		return "", fault.Invalid
	}
	var zone string
	if e := s.db.Row(ctx, "SELECT zone FROM dns_providers WHERE id=$1", v.ProviderID).Scan(&zone); e != nil {
		return "", e
	}
	for i, domain := range v.Domains {
		plain := strings.TrimPrefix(domain, "*.")
		clean, e := website.Domain(plain)
		if e != nil {
			return "", e
		}
		if strings.HasPrefix(domain, "*.") {
			clean = "*." + clean
		}
		if !dns.IsSubDomain(zone, dns.Fqdn(strings.TrimPrefix(clean, "*."))) {
			return "", fault.Invalid
		}
		v.Domains[i] = clean
	}
	if v.ID == "" {
		e := s.db.Row(ctx, "INSERT INTO certificate_orders(name,account_id,provider_id,domains,auto_renew,renew_before_days) VALUES($1,$2,$3,$4,$5,$6) RETURNING id::text", v.Name, v.AccountID, v.ProviderID, v.Domains, v.AutoRenew, v.RenewBefore).Scan(&v.ID)
		return v.ID, e
	}
	tag, e := s.db.Exec(ctx, "UPDATE certificate_orders SET name=$2,auto_renew=$3,renew_before_days=$4,version=version+1 WHERE id=$1 AND version=$5 AND status<>'processing'", v.ID, v.Name, v.AutoRenew, v.RenewBefore, v.Version)
	if e == nil && tag.RowsAffected() != 1 {
		e = fault.Conflict
	}
	return v.ID, e
}
func (s *Service) RetryOrder(ctx context.Context, id string, version int64) error {
	tag, e := s.db.Exec(ctx, "UPDATE certificate_orders SET status='pending',next_attempt=now(),version=version+1 WHERE id=$1 AND version=$2 AND status<>'processing'", id, version)
	if e == nil && tag.RowsAffected() != 1 {
		e = fault.Conflict
	}
	return e
}
