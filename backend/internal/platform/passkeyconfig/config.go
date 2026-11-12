package passkeyconfig

import (
	"golang.org/x/net/publicsuffix"
	"net"
	"net/url"
	"next-terminal/backend/internal/platform/fault"
	"strings"
)

// ValidatePasskeyConfig deliberately requires HTTPS, including development.
// RP identity is explicit and must never be inferred from request headers.
func Validate(rp string, origins []string) error {
	if rp == "" || rp != strings.ToLower(rp) || len(rp) > 253 || len(origins) == 0 || len(origins) > 16 || net.ParseIP(rp) != nil {
		return fault.Invalid
	}
	for _, label := range strings.Split(rp, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fault.Invalid
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return fault.Invalid
			}
		}
	}
	if rp != "localhost" && !strings.HasSuffix(rp, ".localhost") {
		if _, e := publicsuffix.EffectiveTLDPlusOne(rp); e != nil {
			return fault.Invalid
		}
	}
	seen := map[string]bool{}
	for _, origin := range origins {
		u, e := url.Parse(origin)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || seen[origin] || origin != u.Scheme+"://"+u.Host {
			return fault.Invalid
		}
		host := u.Hostname()
		if host != strings.ToLower(host) || (host != rp && !strings.HasSuffix(host, "."+rp)) {
			return fault.Invalid
		}
		if port := u.Port(); port != "" {
			n := 0
			for _, c := range port {
				if c < '0' || c > '9' {
					return fault.Invalid
				}
				n = n*10 + int(c-'0')
				if n > 65535 {
					return fault.Invalid
				}
			}
			if n == 0 {
				return fault.Invalid
			}
		}
		seen[origin] = true
	}
	return nil
}
