package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func ldapFixtureCertificate(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated LDAP test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	ca, e := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "LDAP fixture"}, NotBefore: root.NotBefore, NotAfter: root.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	return tls.Certificate{Certificate: [][]byte{der, ca}, PrivateKey: key}, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca}))
}

func TestLDAPTLSAndUserVerification(t *testing.T) {
	certificate, ca := ldapFixtureCertificate(t)
	for _, mode := range []string{"ldaps", "starttls", "untrusted", "hostname", "reject-starttls", "bad-service", "bad-user", "empty-user-password", "duplicate", "missing-id", "outside-base", "large-response", "injection", "object-guid", "probe", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			config := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				raw, e := listener.Accept()
				if e != nil {
					return
				}
				defer raw.Close()
				raw.SetDeadline(time.Now().Add(3 * time.Second))
				var conn net.Conn = raw
				secured := false
				if mode != "starttls" && mode != "reject-starttls" {
					c := tls.Server(raw, config)
					if c.Handshake() != nil {
						return
					}
					conn = c
					secured = true
				}
				send := func(id int64, op *ber.Packet) {
					packet := ber.NewSequence("message")
					packet.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "id"))
					packet.AppendChild(op)
					conn.Write(packet.Bytes())
				}
				status := func(id int64, tag ber.Tag, code int64) {
					op := ber.Encode(ber.ClassApplication, ber.TypeConstructed, tag, nil, "result")
					op.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, code, "code"))
					op.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "dn"))
					op.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "message"))
					send(id, op)
				}
				for {
					packet, e := ber.ReadPacket(conn)
					if e != nil {
						return
					}
					if len(packet.Children) < 2 {
						return
					}
					id := packet.Children[0].Value.(int64)
					op := packet.Children[1]
					switch op.Tag {
					case 23:
						if mode == "reject-starttls" {
							status(id, 24, 53)
							return
						}
						status(id, 24, 0)
						c := tls.Server(conn, config)
						if c.Handshake() != nil {
							return
						}
						conn = c
						secured = true
					case 0:
						if !secured {
							t.Error("credentials sent before TLS")
							return
						}
						dn := op.Children[1].Value.(string)
						password := op.Children[2].Data.String()
						code := int64(0)
						if dn == "cn=service,dc=example,dc=com" {
							if password != "service-password" {
								code = 49
							}
						} else if password != "user-password" {
							code = 49
						}
						status(id, 1, code)
					case 3:
						filter, err := ldap.DecompileFilter(op.Children[6])
						if err != nil {
							t.Error(err)
							return
						}
						if mode == "injection" && !strings.Contains(filter, `uid=a\2a\29\28uid=\2a\29`) {
							t.Error("filter input not escaped")
						}
						dn := "uid=alice,dc=example,dc=com"
						if mode == "probe" {
							dn = "dc=example,dc=com"
						}
						if mode == "outside-base" {
							dn = "uid=alice,dc=other,dc=com"
						}
						entry := ber.Encode(ber.ClassApplication, ber.TypeConstructed, 4, nil, "entry")
						entry.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, dn, "dn"))
						attrs := ber.NewSequence("attrs")
						if mode != "missing-id" {
							attr := ber.NewSequence("attr")
							name, value := "entryUUID", "9d20fd4b-68b4-48cd-98fd-2e1394166a7f"
							if mode == "large-response" {
								value = strings.Repeat("x", 1024*1024+1)
							}
							if mode == "object-guid" {
								name = "objectGUID"
								value = "0123456789abcdef"
							}
							attr.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "name"))
							values := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "values")
							values.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, value, "value"))
							attr.AppendChild(values)
							attrs.AppendChild(attr)
						}
						entry.AppendChild(attrs)
						send(id, entry)
						if mode == "duplicate" {
							send(id, entry)
						}
						status(id, 5, 0)
					default:
						return
					}
				}
			}()
			p := LDAPProvider{Name: "fixture", URL: "ldaps://" + listener.Addr().String(), BaseDN: "dc=example,dc=com", BindDN: "cn=service,dc=example,dc=com", SearchFilter: "(objectClass=person)", LoginAttribute: "uid", IdentityAttribute: "entryUUID", CAPEM: ca}
			if mode == "starttls" || mode == "reject-starttls" {
				p.URL = strings.Replace(p.URL, "ldaps:", "ldap:", 1)
			}
			if mode == "untrusted" {
				p.CAPEM = ""
			}
			if mode == "hostname" {
				p.URL = strings.Replace(p.URL, "127.0.0.1", "localhost", 1)
			}
			if mode == "object-guid" {
				p.IdentityAttribute = "objectGUID"
			}
			service, username, password := "service-password", "alice", "user-password"
			if mode == "bad-service" {
				service = "incorrect"
			}
			if mode == "bad-user" {
				password = "incorrect"
			}
			if mode == "empty-user-password" {
				password = ""
			}
			if mode == "injection" {
				username = "a*)(uid=*)"
			}
			if mode == "probe" {
				username = ""
				password = ""
			}
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "cancelled" {
				cancel()
			}
			subject, err := VerifyLDAP(ctx, p, service, username, password)
			cancel()
			listener.Close()
			wg.Wait()
			good := mode == "ldaps" || mode == "starttls" || mode == "injection" || mode == "object-guid" || mode == "probe"
			if good {
				if err != nil {
					t.Fatal("valid LDAP rejected", err)
				}
				if mode != "probe" && subject == "" {
					t.Fatal("stable identity missing")
				}
			} else if err == nil {
				t.Fatal("unsafe LDAP accepted", subject)
			}
		})
	}
}

func TestLDAPConfigurationValidation(t *testing.T) {
	p := LDAPProvider{Name: "test", URL: "ldaps://ldap.example.com:636", BaseDN: "dc=example,dc=com", BindDN: "cn=service,dc=example,dc=com", SearchFilter: "(objectClass=person)", LoginAttribute: "uid", IdentityAttribute: "entryUUID"}
	if ValidateLDAPProvider(p) != nil {
		t.Fatal("valid config")
	}
	for _, edit := range []func(*LDAPProvider){func(p *LDAPProvider) { p.URL = "http://ldap.example.com" }, func(p *LDAPProvider) { p.URL += "/path" }, func(p *LDAPProvider) { p.BaseDN = "" }, func(p *LDAPProvider) { p.SearchFilter = "(invalid" }, func(p *LDAPProvider) { p.LoginAttribute = "uid)(objectClass=*" }, func(p *LDAPProvider) { p.IdentityAttribute = "mail" }, func(p *LDAPProvider) { p.CAPEM = "private-key-or-garbage" }} {
		v := p
		edit(&v)
		if ValidateLDAPProvider(v) == nil {
			t.Fatal("invalid config accepted")
		}
	}
}
