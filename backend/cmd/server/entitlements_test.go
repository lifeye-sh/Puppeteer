package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfiguredEmptyEntitlementFileFailsClosed(t *testing.T) {
	t.Setenv("NT_ENTITLEMENT_MODE", "local")
	t.Setenv("NT_ENTITLEMENT_TRUST_FILE", "")
	t.Setenv("NT_ENTITLEMENT_LOCAL_FILE", "")
	if p, _, e := loadEntitlements(); e != nil || p == nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "policy.json")
	if e := os.WriteFile(path, nil, 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("NT_ENTITLEMENT_LOCAL_FILE", path)
	if _, _, e := loadEntitlements(); e == nil {
		t.Fatal("configured empty file became unlimited")
	}
	if e := os.WriteFile(path, []byte(`{"features":{},"quotas":{"users":0,"resources":0,"sessions":0}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e := loadEntitlements(); e != nil {
		t.Fatal(e)
	}
}
