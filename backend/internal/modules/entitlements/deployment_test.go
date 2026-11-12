package entitlements

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestDeploymentTrustFailClosed(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	valid := []byte(`{"keys":{"fixture":"` + base64.StdEncoding.EncodeToString(pub) + `"}}`)
	p, v, e := Deployment("signed", nil, valid)
	if e != nil || p != nil || v == nil {
		t.Fatal(e)
	}
	for _, raw := range []string{`{}`, `{"keys":{}}`, `{"keys":null}`, `{"keys":{"fixture":"AA=="}}`, `{"keys":{"fixture":"bad","fixture":"bad"}}`} {
		if _, _, e := Deployment("signed", nil, []byte(raw)); e == nil {
			t.Fatal(raw)
		}
	}
	if _, _, e = Deployment("local", nil, valid); e == nil {
		t.Fatal("ignored trust file")
	}
	if _, _, e = Deployment("signed", []byte(`{}`), valid); e == nil {
		t.Fatal("mixed mode")
	}
	if _, _, e = Deployment("typo", nil, nil); e == nil {
		t.Fatal("unknown mode")
	}
	p, v, e = Deployment("", nil, nil)
	if e != nil || p == nil || v != nil {
		t.Fatal(e)
	}
}
