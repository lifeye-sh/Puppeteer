package application

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
	"net/http"
	"sync"
	"testing"
	"time"
)

func ldapLifecycleCertificate(t *testing.T) (tls.Certificate, string) {
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

func ldapLifecycleServer(t *testing.T, extraAttributes ...map[string][]string) (string, string) {
	certificate, ca := ldapLifecycleCertificate(t)
	config := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			raw, e := listener.Accept()
			if e != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer raw.Close()
				raw.SetDeadline(time.Now().Add(5 * time.Second))
				var conn net.Conn = tls.Server(raw, config)
				secured := true
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
						_, err := ldap.DecompileFilter(op.Children[6])
						if err != nil {
							t.Error(err)
							return
						}

						dn := "uid=alice,dc=example,dc=com"
						if op.Children[1].Value.(int64) == 0 {
							dn = "dc=example,dc=com"
						}

						entry := ber.Encode(ber.ClassApplication, ber.TypeConstructed, 4, nil, "entry")
						entry.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, dn, "dn"))
						attrs := ber.NewSequence("attrs")
						if true {
							attr := ber.NewSequence("attr")
							name, value := "entryUUID", "9d20fd4b-68b4-48cd-98fd-2e1394166a7f"

							attr.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "name"))
							values := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "values")
							values.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, value, "value"))
							attr.AppendChild(values)
							attrs.AppendChild(attr)
						}
						nameAttr := ber.NewSequence("attr")
						nameAttr.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "uid", "name"))
						nameValues := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "values")
						nameValues.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "alice", "value"))
						nameAttr.AppendChild(nameValues)
						attrs.AppendChild(nameAttr)
						for _, extra := range extraAttributes {
							for name, rawValues := range extra {
								attr := ber.NewSequence("attribute")
								attr.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "name"))
								values := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "values")
								for _, value := range rawValues {
									values.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, value, "value"))
								}
								attr.AppendChild(values)
								attrs.AppendChild(attr)
							}
						}
						entry.AppendChild(attrs)
						send(id, entry)

						status(id, 5, 0)
					default:
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { listener.Close(); wg.Wait() })
	return "ldaps://" + listener.Addr().String(), ca
}
func TestR13LDAPLoginLifecycle(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	const secure = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, secure)
	admin := loginAs(t, srv, "accept-admin")
	changeLoginDuration(t, srv, admin, 7)
	uid := postID(t, srv, "/api/admin/users", userInput("ldap-owner", nil, nil), admin)
	owner := loginAs(t, srv, "ldap-owner")
	postID(t, srv, "/api/admin/users", userInput("ldap-other", nil, nil), admin)
	other := loginAs(t, srv, "ldap-other")
	address, ca := ldapLifecycleServer(t)
	input := map[string]any{"name": "LDAP lifecycle", "url": address, "caPEM": ca, "baseDN": "dc=example,dc=com", "bindDN": "cn=service,dc=example,dc=com", "bindPassword": "service-password", "searchFilter": "(objectClass=person)", "loginAttribute": "uid", "identityAttribute": "entryUUID", "enabled": true, "bindingEnabled": true, "password": testPassword}
	pid := postID(t, srv, "/api/admin/ldap-providers", input, admin)
	bind := func(c *http.Cookie) result {
		return call(t, srv.URL, "POST", "/api/account/ldap-providers/"+pid+"/bind", map[string]string{"username": "alice", "directoryPassword": "user-password", "password": testPassword}, c, secure)
	}
	loginBody := map[string]string{"source": "ldap", "providerId": pid, "username": "alice", "password": "user-password"}
	login := func() result {
		a.mu.Lock()
		a.attempts = map[string]attempt{}
		a.mu.Unlock()
		return call(t, srv.URL, "POST", "/api/ldap/login", loginBody, nil, secure)
	}
	expect(t, login(), 400)
	expect(t, bind(owner), 200)
	expect(t, bind(other), 409)
	r := login()
	expect(t, r, 200)
	if r.cookie == nil || !r.cookie.Secure {
		t.Fatal("LDAP cookie must be secure")
	}
	session := r.cookie
	assertCookieDuration(t, session, 7*60)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, session, origin), 200)
	var id string
	if e := pool.QueryRow(ctx, `SELECT id FROM ldap_identities WHERE user_id=$1`, uid).Scan(&id); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/account/ldap-identities/"+id, map[string]string{"password": testPassword}, other, secure), 403)
	expect(t, call(t, srv.URL, "DELETE", "/api/account/ldap-identities/"+id, map[string]string{"password": testPassword}, owner, secure), 200)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, session, origin), 401)
	expect(t, login(), 400)
	expect(t, bind(owner), 200)
	r = login()
	expect(t, r, 200)
	session = r.cookie
	input["version"] = 1
	input["enabled"] = false
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ldap-providers/"+pid, input, admin, origin), 200)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, session, origin), 401)
	expect(t, login(), 400)
	input["version"] = 2
	input["enabled"] = true
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ldap-providers/"+pid, input, admin, origin), 200)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, session, origin), 401)
	loginBody["source"] = "password"
	expect(t, login(), 422)
	loginBody["source"] = "ldap"
	loginBody["password"] = "bad"
	expect(t, login(), 400)
	loginBody["password"] = "user-password"
	pool.Exec(ctx, `DELETE FROM login_locks`)
	a.config.CaptchaRequired = true
	expect(t, login(), 400)
	a.config.CaptchaRequired = false
	if e := a.db.Within(ctx, func(ctx context.Context) error { return a.identity.RevokeAll(ctx, uid) }); e != nil {
		t.Fatal(e)
	}
	r = login()
	expect(t, r, 200)
	if e := a.db.Within(ctx, func(ctx context.Context) error { return a.identity.QuarantineRestore(ctx) }); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, r.cookie, origin), 401)
	expect(t, login(), 400)
}
