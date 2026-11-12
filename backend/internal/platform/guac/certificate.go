package guac

import (
	"encoding/hex"
	"strings"
)

// CertificateFingerprints converts compact digests to the colon-separated
// representation required by FreeRDP 2.x (also accepted by FreeRDP 3.x).
// It changes only representation, never the pinned digest or trust policy.
func CertificateFingerprints(value string) string {
	entries := strings.Split(value, ",")
	for i, entry := range entries {
		algorithm, digest, ok := strings.Cut(strings.TrimSpace(entry), ":")
		if !ok {
			continue
		}
		compact := strings.ReplaceAll(digest, ":", "")
		size := map[string]int{"sha256": 32, "sha1": 20, "sha512": 64}[strings.ToLower(algorithm)]
		decoded, err := hex.DecodeString(compact)
		if err != nil || size == 0 || len(decoded) != size {
			continue
		}
		pairs := make([]string, size)
		for j := range pairs {
			pairs[j] = strings.ToLower(compact[j*2 : j*2+2])
		}
		entries[i] = strings.ToLower(algorithm) + ":" + strings.Join(pairs, ":")
	}
	return strings.Join(entries, ",")
}
