package migrations

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestBrandingRevisionChecksums(t *testing.T) {
	for version, pair := range brandingRevisions {
		names, err := files.ReadDir(".")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, name := range names {
			var n int
			if _, err := fmt.Sscanf(name.Name(), "%d_", &n); err != nil || n != version {
				continue
			}
			data, err := files.ReadFile(name.Name())
			if err != nil {
				t.Fatal(err)
			}
			sum := fmt.Sprintf("%x", sha256.Sum256(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))))
			if !knownBrandingRevision(version, pair[0], sum) {
				t.Fatalf("canonical migration %d changed", version)
			}
			if !knownBrandingRevision(version, brandingLineEndingRevisions[version], sum) {
				t.Fatal("alternate published line ending rejected")
			}
			found = true
		}
		if !found {
			t.Fatalf("migration %d missing", version)
		}
		if knownBrandingRevision(version, "tampered", pair[1]) || knownBrandingRevision(version, pair[0], "tampered") {
			t.Fatal("accepted arbitrary SQL change")
		}
	}
	if knownBrandingRevision(109, "", " ") {
		t.Fatal("accepted unrelated migration")
	}
}
