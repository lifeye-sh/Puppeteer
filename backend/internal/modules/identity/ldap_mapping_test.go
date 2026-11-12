package identity

import (
	"github.com/go-ldap/ldap/v3"
	"testing"
)

func TestLDAPOptionalClaims(t *testing.T) {
	p := LDAPProvider{PropertyMapping: &LDAPPropertyMapping{Username: "uid", Nickname: "cn", Mail: "mail"}}
	for _, tc := range []struct {
		name    string
		attrs   map[string][]string
		wantErr bool
	}{
		{"missing", map[string][]string{}, false},
		{"valid", map[string][]string{"cn": {"Alice 王"}, "mail": {"alice@example.com"}}, false},
		{"duplicate-name", map[string][]string{"cn": {"Alice", "Other"}}, true},
		{"duplicate-mail", map[string][]string{"mail": {"alice@example.com", "bob@example.com"}}, true},
		{"display-mail", map[string][]string{"mail": {"Alice <alice@example.com>"}}, true},
		{"control", map[string][]string{"cn": {"Alice\nadmin"}}, true},
		{"invalid-utf8", map[string][]string{"cn": {string([]byte{0xff})}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, e := ldapMappedEntry(p, ldap.NewEntry("uid=alice,dc=example,dc=com", tc.attrs), "stable-id", "alice")
			if (e != nil) != tc.wantErr {
				t.Fatal(got, e)
			}
			if e == nil && (got.Username != "alice" || got.Subject != "stable-id") {
				t.Fatal("mapping changed identity")
			}
		})
	}
	for _, m := range []LDAPPropertyMapping{{}, {Username: "uid)(objectClass=*"}, {Username: "uid", Nickname: "*"}, {Username: "uid", Mail: "mail;binary"}} {
		if ValidateLDAPMapping(m) == nil {
			t.Fatal("accepted invalid mapping", m)
		}
	}
}
