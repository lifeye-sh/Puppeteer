package guac

import (
	"strings"
	"testing"
)

func TestCertificateFingerprintsCompatibility(t *testing.T) {
	compact := "sha256:" + strings.Repeat("ab", 32)
	separated := "sha256:" + strings.TrimSuffix(strings.Repeat("ab:", 32), ":")
	for _, input := range []string{compact, separated, strings.ToUpper(compact)} {
		if got := CertificateFingerprints(input); got != separated {
			t.Fatalf("incompatible fingerprint: %s", got)
		}
	}
	for _, invalid := range []string{"", "sha256:abcd", "sha256:" + strings.Repeat("zz", 32), "other:ab"} {
		if got := CertificateFingerprints(invalid); got != invalid {
			t.Fatalf("changed invalid pin: %q", got)
		}
	}
	if got := CertificateFingerprints(compact + "," + separated); got != separated+","+separated {
		t.Fatal("pin list changed")
	}
}
