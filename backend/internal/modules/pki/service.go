package pki

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"next-terminal/backend/internal/platform/secretbox"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
	"strings"
	"time"
)

type Service struct {
	db  *postgres.DB
	key []byte
}

func New(db *postgres.DB, key []byte) *Service { return &Service{db, key} }

type Certificate struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	Source      string     `json:"source"`
	PEM         string     `json:"pem"`
	Fingerprint string     `json:"fingerprint"`
	DNSNames    []string   `json:"dnsNames"`
	NotBefore   time.Time  `json:"notBefore"`
	NotAfter    time.Time  `json:"notAfter"`
	ParentID    string     `json:"parentId"`
	UserID      string     `json:"userId"`
	RevokedAt   *time.Time `json:"revokedAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt"`
	Version     int64      `json:"version"`
}

const columns = `id::text,name,kind,source,pem,fingerprint,dns_names,not_before,not_after,coalesce(parent_id::text,''),coalesce(user_id::text,''),revoked_at,last_used_at,version`

type scanner interface{ Scan(...any) error }

func scan(row scanner) (v Certificate, e error) {
	e = row.Scan(&v.ID, &v.Name, &v.Kind, &v.Source, &v.PEM, &v.Fingerprint, &v.DNSNames, &v.NotBefore, &v.NotAfter, &v.ParentID, &v.UserID, &v.RevokedAt, &v.LastUsedAt, &v.Version)
	return
}
func (s *Service) List(ctx context.Context) ([]Certificate, error) {
	rows, e := s.db.Rows(ctx, "SELECT "+columns+" FROM certificates ORDER BY created_at DESC")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Certificate{}
	for rows.Next() {
		v, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Get(ctx context.Context, id string) (Certificate, error) {
	return scan(s.db.Row(ctx, "SELECT "+columns+" FROM certificates WHERE id=$1", id))
}
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}
func pair(certPEM, keyPEM []byte) (tls.Certificate, error) {
	p, e := tls.X509KeyPair(certPEM, keyPEM)
	if e != nil {
		return p, fault.Invalid
	}
	if p.Leaf == nil {
		p.Leaf, e = x509.ParseCertificate(p.Certificate[0])
	}
	return p, e
}
func (s *Service) TLS(ctx context.Context, id string) (tls.Certificate, error) {
	v, e := s.Get(ctx, id)
	if e != nil {
		return tls.Certificate{}, e
	}
	if v.RevokedAt != nil || time.Now().Before(v.NotBefore) || !time.Now().Before(v.NotAfter) {
		return tls.Certificate{}, fault.Denied
	}
	var encrypted []byte
	if e = s.db.Row(ctx, "SELECT key_sealed FROM certificates WHERE id=$1", id).Scan(&encrypted); e != nil {
		return tls.Certificate{}, e
	}
	key, e := secretbox.Open(s.key, encrypted, "certificate:"+id)
	if e != nil {
		return tls.Certificate{}, e
	}
	defer clear(key)
	return pair([]byte(v.PEM), key)
}

// Store must be called inside the caller's audited transaction.
func (s *Service) Store(ctx context.Context, v Certificate, keyPEM []byte) (string, error) {
	if strings.TrimSpace(v.Name) == "" || len(v.Name) > 100 || len(v.PEM) > 256*1024 || len(keyPEM) > 32*1024 {
		return "", fault.Invalid
	}
	p, e := pair([]byte(v.PEM), keyPEM)
	if e != nil {
		return "", e
	}
	leaf := p.Leaf
	if leaf.NotAfter.Before(time.Now()) || leaf.NotBefore.After(time.Now().Add(time.Minute)) {
		return "", fault.Invalid
	}
	if v.Kind == "client_ca" {
		if !leaf.IsCA || leaf.KeyUsage&x509.KeyUsageCertSign == 0 {
			return "", fault.Invalid
		}
	} else if v.Kind == "server" {
		if leaf.IsCA || len(leaf.DNSNames)+len(leaf.IPAddresses) == 0 {
			return "", fault.Invalid
		}
		if len(leaf.ExtKeyUsage) > 0 {
			ok := false
			for _, usage := range leaf.ExtKeyUsage {
				if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
					ok = true
				}
			}
			if !ok {
				return "", fault.Invalid
			}
		}
	} else if v.Kind != "client" {
		return "", fault.Invalid
	}
	v.DNSNames = append([]string{}, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		v.DNSNames = append(v.DNSNames, ip.String())
	}
	v.Fingerprint = Fingerprint(leaf)
	if v.ID == "" {
		if e = s.db.Row(ctx, `SELECT gen_random_uuid()::text`).Scan(&v.ID); e != nil {
			return "", e
		}
		encrypted, e := secretbox.Seal(s.key, keyPEM, "certificate:"+v.ID)
		if e != nil {
			return "", e
		}
		_, e = s.db.Exec(ctx, `INSERT INTO certificates(id,name,kind,source,pem,key_sealed,fingerprint,dns_names,not_before,not_after,parent_id,user_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,nullif($11,'')::uuid,nullif($12,'')::uuid)`, v.ID, v.Name, v.Kind, v.Source, v.PEM, encrypted, v.Fingerprint, v.DNSNames, leaf.NotBefore, leaf.NotAfter, v.ParentID, v.UserID)
		return v.ID, e
	}
	encrypted, e := secretbox.Seal(s.key, keyPEM, "certificate:"+v.ID)
	if e != nil {
		return "", e
	}
	tag, e := s.db.Exec(ctx, `UPDATE certificates SET name=$2,pem=$3,key_sealed=$4,fingerprint=$5,dns_names=$6,not_before=$7,not_after=$8,version=version+1 WHERE id=$1 AND version=$9 AND kind='server' AND revoked_at IS NULL`, v.ID, v.Name, v.PEM, encrypted, v.Fingerprint, v.DNSNames, leaf.NotBefore, leaf.NotAfter, v.Version)
	if e == nil && tag.RowsAffected() != 1 {
		e = fault.Conflict
	}
	return v.ID, e
}

func Generate(name string, domains []string, days int, ca bool, parent *tls.Certificate) (certPEM, keyPEM []byte, err error) {
	if days < 1 || days > 3650 || len(name) == 0 || len(name) > 100 || len(domains) > 100 {
		return nil, nil, fault.Invalid
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, nil, e
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return nil, nil, e
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Duration(days) * 24 * time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, domain := range domains {
		if ip := net.ParseIP(domain); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			if strings.ContainsAny(domain, " /\\\r\n") || domain == "" {
				return nil, nil, fault.Invalid
			}
			template.DNSNames = append(template.DNSNames, domain)
		}
	}
	if ca {
		template.IsCA = true
		template.KeyUsage |= x509.KeyUsageCertSign | x509.KeyUsageCRLSign
		template.ExtKeyUsage = nil
		template.MaxPathLenZero = true
	}
	signer := crypto.Signer(key)
	issuer := template
	if parent != nil {
		issuer = parent.Leaf
		var ok bool
		signer, ok = parent.PrivateKey.(crypto.Signer)
		if !ok || issuer == nil || !issuer.IsCA {
			return nil, nil, fault.Invalid
		}
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		if template.NotAfter.After(issuer.NotAfter) {
			template.NotAfter = issuer.NotAfter
		}
	}
	der, e := x509.CreateCertificate(rand.Reader, template, issuer, &key.PublicKey, signer)
	if e != nil {
		return nil, nil, e
	}
	private, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		return nil, nil, e
	}
	defer clear(private)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), nil
}
func (s *Service) IssueClient(ctx context.Context, name, caID, user string, days int) (string, error) {
	if days == 0 {
		var e error
		days, e = system.New(s.db).ClientCertificateDays(ctx)
		if e != nil {
			return "", e
		}
	}
	if days < 1 || days > 825 {
		return "", fault.Invalid
	}
	ca, e := s.Get(ctx, caID)
	if e != nil {
		return "", e
	}
	if ca.Kind != "client_ca" {
		return "", fault.Invalid
	}
	parent, e := s.TLS(ctx, caID)
	if e != nil {
		return "", e
	}
	cert, key, e := Generate(name, nil, days, false, &parent)
	if e != nil {
		return "", e
	}
	defer clear(key)
	return s.Store(ctx, Certificate{Name: name, Kind: "client", Source: "issued", PEM: string(cert), ParentID: caID, UserID: user}, key)
}
func (s *Service) ExportClient(ctx context.Context, id, password string) ([]byte, error) {
	if len(password) < 12 || len(password) > 128 {
		return nil, fault.Invalid
	}
	v, e := s.Get(ctx, id)
	if e != nil {
		return nil, e
	}
	if v.Kind != "client" {
		return nil, fault.Invalid
	}
	p, e := s.TLS(ctx, id)
	if e != nil {
		return nil, e
	}
	ca, e := s.TLS(ctx, v.ParentID)
	if e != nil {
		return nil, e
	}
	return pkcs12.Modern.Encode(p.PrivateKey, p.Leaf, []*x509.Certificate{ca.Leaf}, password)
}
func (s *Service) Revoke(ctx context.Context, id string, version int64) error {
	tag, e := s.db.Exec(ctx, "UPDATE certificates SET revoked_at=now(),version=version+1 WHERE id=$1 AND version=$2 AND revoked_at IS NULL", id, version)
	if e == nil && tag.RowsAffected() != 1 {
		e = fault.Conflict
	}
	return e
}
func (s *Service) ClientIdentity(ctx context.Context, caID string, chain []*x509.Certificate) (string, error) {
	if len(chain) == 0 {
		return "", fault.Denied
	}
	ca, e := s.Get(ctx, caID)
	if e != nil {
		return "", e
	}
	if ca.Kind != "client_ca" || ca.RevokedAt != nil {
		return "", fault.Denied
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(ca.PEM)) {
		return "", fault.Denied
	}
	intermediates := x509.NewCertPool()
	for _, c := range chain[1:] {
		intermediates.AddCert(c)
	}
	if _, e = chain[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); e != nil {
		return "", fault.Denied
	}
	var id, user string
	if e = s.db.Row(ctx, `SELECT id::text,user_id::text FROM certificates WHERE fingerprint=$1 AND kind='client' AND parent_id=$2 AND revoked_at IS NULL AND not_before<=now() AND not_after>now()`, Fingerprint(chain[0]), caID).Scan(&id, &user); e != nil {
		return "", fault.Denied
	}
	_, e = s.db.Exec(ctx, `UPDATE certificates SET last_used_at=now() WHERE id=$1 AND (last_used_at IS NULL OR last_used_at<now()-interval '1 minute')`, id)
	return user, e
}
