package identity

import (
	"context"
	"crypto/tls"
	"next-terminal/backend/internal/platform/ldapconfig"

	"encoding/hex"

	"errors"
	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
	"net"
	"net/url"

	"strings"
	"time"
)

type LDAPProvider = ldapconfig.Provider
type LDAPPropertyMapping = ldapconfig.Mapping

var ErrLDAPVerification = errors.New("LDAP verification failed")
var ValidateLDAPProvider = ldapconfig.Validate
var ValidateLDAPMapping = ldapconfig.ValidateMapping
var ldapRoots = ldapconfig.Roots

// Bound the entire exchange, including malformed or excessive LDAP responses.
type ldapBoundedConn struct {
	net.Conn
	remaining int
}

func (c *ldapBoundedConn) Read(b []byte) (int, error) {
	if c.remaining <= 0 {
		return 0, ErrLDAPVerification
	}
	if len(b) > c.remaining {
		b = b[:c.remaining]
	}
	n, e := c.Conn.Read(b)
	c.remaining -= n
	return n, e
}

// VerifyLDAP never accepts anonymous bind and never transmits credentials before TLS.
// With empty test credentials it checks service bind and Base DN visibility only.
func VerifyLDAP(ctx context.Context, p LDAPProvider, servicePassword, username, password string) (string, error) {
	if ValidateLDAPProvider(p) != nil || servicePassword == "" || len(servicePassword) > 65536 || len(username) > 256 || len(password) > 1024 || strings.ContainsAny(username, "\x00\r\n") || strings.ContainsRune(password, 0) || ((username == "") != (password == "")) {
		return "", ErrLDAPVerification
	}
	conn, cleanup, err := openLDAPConnection(ctx, p, servicePassword, 1024*1024)
	if err != nil {
		return "", err
	}
	defer cleanup()
	filter := "(objectClass=*)"
	scope := ldap.ScopeBaseObject
	attributes := []string{"1.1"}
	if username != "" {
		filter = "(&" + p.SearchFilter + "(" + p.LoginAttribute + "=" + ldap.EscapeFilter(username) + "))"
		scope = ldap.ScopeWholeSubtree
		attributes = []string{p.IdentityAttribute}
	}
	result, err := conn.Search(ldap.NewSearchRequest(p.BaseDN, scope, ldap.NeverDerefAliases, 2, 5, false, filter, attributes, nil))
	if err != nil || len(result.Referrals) != 0 || len(result.Entries) != 1 {
		return "", ErrLDAPVerification
	}
	entry := result.Entries[0]
	base, _ := ldap.ParseDN(p.BaseDN)
	dn, err := ldap.ParseDN(entry.DN)
	if err != nil || !(base.EqualFold(dn) || base.AncestorOfFold(dn)) {
		return "", ErrLDAPVerification
	}
	if username == "" {
		if !base.EqualFold(dn) {
			return "", ErrLDAPVerification
		}
		return "", nil
	}
	values := entry.GetEqualFoldRawAttributeValues(p.IdentityAttribute)
	if len(values) != 1 {
		return "", ErrLDAPVerification
	}
	var subject string
	if p.IdentityAttribute == "entryUUID" {
		id, e := uuid.Parse(string(values[0]))
		if e != nil || id == uuid.Nil {
			return "", ErrLDAPVerification
		}
		subject = "entryUUID:" + id.String()
	} else {
		if len(values[0]) != 16 || string(values[0]) == string(make([]byte, 16)) {
			return "", ErrLDAPVerification
		}
		subject = "objectGUID:" + hex.EncodeToString(values[0])
	}
	if err = conn.Bind(entry.DN, password); err != nil {
		return "", ErrLDAPVerification
	}
	return subject, nil
}

func openLDAPConnection(ctx context.Context, p LDAPProvider, servicePassword string, maxBytes int) (result *ldap.Conn, cleanup func(), err error) {
	if ValidateLDAPProvider(p) != nil || servicePassword == "" || len(servicePassword) > 65536 {
		return nil, nil, ErrLDAPVerification
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer func() {
		if err != nil {
			cancel()
		}
	}()
	u, _ := url.Parse(p.URL)
	port := u.Port()
	if port == "" {
		port = "389"
		if u.Scheme == "ldaps" {
			port = "636"
		}
	}
	roots, err := ldapRoots(p.CAPEM)
	if err != nil {
		return nil, nil, ErrLDAPVerification
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname(), RootCAs: roots}
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		return nil, nil, ErrLDAPVerification
	}
	defer func() {
		if err != nil {
			raw.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer func() {
		if err != nil {
			stop()
		}
	}()
	deadline, _ := ctx.Deadline()
	if raw.SetDeadline(deadline) != nil {
		return nil, nil, ErrLDAPVerification
	}
	var wire net.Conn = raw
	if u.Scheme == "ldaps" {
		secured := tls.Client(raw, config)
		if secured.HandshakeContext(ctx) != nil {
			return nil, nil, ErrLDAPVerification
		}
		wire = secured
	}
	conn := ldap.NewConn(&ldapBoundedConn{Conn: wire, remaining: maxBytes}, u.Scheme == "ldaps")
	conn.Start()
	defer func() {
		if err != nil {
			conn.Close()
		}
	}()
	conn.SetTimeout(5 * time.Second)
	if u.Scheme == "ldap" {
		if err = conn.StartTLS(config); err != nil {
			return nil, nil, ErrLDAPVerification
		}
	}
	if err = conn.Bind(p.BindDN, servicePassword); err != nil {
		return nil, nil, ErrLDAPVerification
	}
	return conn, func() { conn.Close(); stop(); raw.Close(); cancel() }, nil
}
