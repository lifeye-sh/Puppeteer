package ldapconfig

import (
	"crypto/x509"
	"encoding/pem"
	"github.com/go-ldap/ldap/v3"
	"net/url"
	"next-terminal/backend/internal/platform/fault"
	"regexp"
	"strconv"
	"strings"
)

type Provider struct {
	Managed           bool     `json:"settingsManaged"`
	SearchSizeLimit   *int     `json:"searchSizeLimit,omitempty"`
	PropertyMapping   *Mapping `json:"propertyMapping,omitempty"`
	Enabled           bool     `json:"enabled"`
	BindingEnabled    bool     `json:"bindingEnabled"`
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	URL               string   `json:"url"`
	BaseDN            string   `json:"baseDN"`
	BindDN            string   `json:"bindDN"`
	SearchFilter      string   `json:"searchFilter"`
	LoginAttribute    string   `json:"loginAttribute"`
	IdentityAttribute string   `json:"identityAttribute"`
	CAPEM             string   `json:"caPEM"`
	HasSecret         bool     `json:"hasSecret"`
	Version           int64    `json:"version"`
}

type Mapping struct {
	Username string `json:"username"`
	Nickname string `json:"nickname,omitempty"`
	Mail     string `json:"mail,omitempty"`
}

func (p Provider) DirectoryLimit() int {
	if p.SearchSizeLimit == nil {
		return 10000
	}
	return *p.SearchSizeLimit
}

func ValidateMapping(m Mapping) error {
	if !ldapAttributePattern.MatchString(m.Username) {
		return fault.Invalid
	}
	for _, attr := range []string{m.Nickname, m.Mail} {
		if attr != "" && !ldapAttributePattern.MatchString(attr) {
			return fault.Invalid
		}
	}
	return nil
}

var ldapAttributePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{0,63}$`)

func Validate(p Provider) error {
	if p.DirectoryLimit() < 1 || p.DirectoryLimit() > 10000 {
		return fault.Invalid
	}
	if m := p.PropertyMapping; m != nil && (ValidateMapping(*m) != nil || m.Username != p.LoginAttribute) {
		return fault.Invalid
	}
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 100 || len(p.URL) > 2048 || len(p.BaseDN) > 1024 || len(p.BindDN) > 1024 || len(p.SearchFilter) > 4096 || len(p.CAPEM) > 65536 {
		return fault.Invalid
	}
	u, err := url.Parse(p.URL)
	if err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(p.URL, "\r\n\\#") {
		return fault.Invalid
	}
	if u.Port() != "" {
		port, e := strconv.Atoi(u.Port())
		if e != nil || port < 1 || port > 65535 {
			return fault.Invalid
		}
	}
	for _, value := range []string{p.BaseDN, p.BindDN} {
		dn, e := ldap.ParseDN(value)
		if e != nil || len(dn.RDNs) == 0 || strings.ContainsRune(value, 0) {
			return fault.Invalid
		}
	}
	if !ldapAttributePattern.MatchString(p.LoginAttribute) || (p.IdentityAttribute != "entryUUID" && p.IdentityAttribute != "objectGUID") {
		return fault.Invalid
	}
	if _, err = ldap.CompileFilter(p.SearchFilter); err != nil {
		return fault.Invalid
	}
	_, err = Roots(p.CAPEM)
	return err
}

func Roots(extra string) (*x509.CertPool, error) {
	if extra == "" {
		return nil, nil
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	rest := []byte(extra)
	count := 0
	for len(strings.TrimSpace(string(rest))) > 0 {
		// Do not accept private keys, non-certificate blocks, or trailing garbage.
		if !strings.HasPrefix(strings.TrimSpace(string(rest)), "-----BEGIN CERTIFICATE-----") {
			return nil, fault.Invalid
		}
		block, tail := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, fault.Invalid
		}
		cert, e := x509.ParseCertificate(block.Bytes)
		if e != nil || !cert.IsCA {
			return nil, fault.Invalid
		}
		roots.AddCert(cert)
		rest = tail
		count++
	}
	if count == 0 {
		return nil, fault.Invalid
	}
	return roots, nil
}
