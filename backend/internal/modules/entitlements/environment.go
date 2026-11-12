package entitlements

import (
	"io"
	"os"
)

// LoadEnvironment is shared by the API and the independent RDP storage process.
// Both must receive the same read-only deployment files, never request input.
func LoadEnvironment() (CapabilityProvider, *Verifier, error) {
	read := func(name string) ([]byte, error) {
		path := os.Getenv(name)
		if path == "" {
			return nil, nil
		}
		f, e := os.Open(path)
		if e != nil {
			return nil, ErrInvalid
		}
		defer f.Close()
		raw, e := io.ReadAll(io.LimitReader(f, 16385))
		if e != nil || len(raw) == 0 || len(raw) > 16384 {
			return nil, ErrInvalid
		}
		return raw, nil
	}
	local, e := read("NT_ENTITLEMENT_LOCAL_FILE")
	if e != nil {
		return nil, nil, e
	}
	trust, e := read("NT_ENTITLEMENT_TRUST_FILE")
	if e != nil {
		return nil, nil, e
	}
	return Deployment(os.Getenv("NT_ENTITLEMENT_MODE"), local, trust)
}
