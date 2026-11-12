package databaseauth

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDualVerifierLegacyAndSCRAM(t *testing.T) {
	v := NewVerifier()
	password, encoded, e := v.Create(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var b passwordBundle
	if e = json.Unmarshal([]byte(encoded), &b); e != nil {
		t.Fatal(e)
	}
	if e = v.Verify(context.Background(), password, b.MySQL); e != nil {
		t.Fatal("legacy MySQL verifier rejected", e)
	}
	if _, e = SCRAMCredentials(b.MySQL); e == nil {
		t.Fatal("legacy verifier pretended to support SCRAM")
	}
	material, e := SCRAMCredentials(encoded)
	if e != nil || material.Iterations != 32768 || len(material.Salt) != 16 || len(material.StoredKey) != 32 || len(material.ServerKey) != 32 {
		t.Fatal("invalid SCRAM material", e)
	}
	if strings.Contains(encoded, password) {
		t.Fatal("password persisted")
	}
	for _, mutate := range []func(*passwordBundle){func(b *passwordBundle) { b.Version = 99 }, func(b *passwordBundle) { b.SCRAM.Iterations = 1 << 30 }, func(b *passwordBundle) { b.SCRAM.StoredKey = nil }} {
		copy := b
		mutate(&copy)
		raw, _ := json.Marshal(copy)
		if _, e = SCRAMCredentials(string(raw)); e == nil {
			t.Fatal("malformed SCRAM accepted")
		}
		if e = v.Verify(context.Background(), password, string(raw)); e == nil {
			t.Fatal("malformed bundle accepted")
		}
	}
}
