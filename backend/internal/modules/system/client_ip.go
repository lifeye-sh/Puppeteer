package system

import (
	"golang.org/x/net/http/httpguts"
	"net/netip"
	"next-terminal/backend/internal/platform/fault"
	"strings"
)

func TrustedProxyPrefixes(raw string) ([]netip.Prefix, error) {
	out := []netip.Prefix{}
	if raw == "" {
		return out, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 1024 {
		return nil, fault.Invalid
	}
	seen := map[netip.Prefix]bool{}
	for _, part := range parts {
		p, e := netip.ParsePrefix(strings.TrimSpace(part))
		if e != nil || p.Addr().Is4In6() {
			return nil, fault.Invalid
		}
		p = p.Masked()
		if !seen[p] {
			out = append(out, p)
			seen[p] = true
		}
	}
	return out, nil
}

func validateClientIP(v ConfigurationValues) error {
	extractor := v.String("ip-extractor")
	if extractor != "direct" {
		if len(extractor) > 64 || strings.ToLower(extractor) != extractor || !httpguts.ValidHeaderFieldName(extractor) {
			return fault.Invalid
		}
		switch extractor {
		case "authorization", "proxy-authorization", "cookie", "set-cookie", "host", "connection", "content-type", "content-length", "x-api-key", "x-auth-token":
			return fault.Invalid
		}
		if strings.HasPrefix(extractor, "x-internal-") || strings.HasPrefix(extractor, "sec-") {
			return fault.Invalid
		}
	}
	prefixes, e := TrustedProxyPrefixes(v.String("ip-trust-list"))
	if e != nil {
		return e
	}
	if extractor != "direct" && len(prefixes) == 0 {
		return fault.Invalid
	}
	return nil
}

func restrictiveClientIP(old, next ConfigurationValues, out ConfigurationValues) error {
	header := next.String("ip-extractor")
	if header != old.String("ip-extractor") {
		header = "direct"
	}
	a, e := TrustedProxyPrefixes(old.String("ip-trust-list"))
	if e != nil {
		return e
	}
	b, e := TrustedProxyPrefixes(next.String("ip-trust-list"))
	if e != nil {
		return e
	}
	result := []string{}
	seen := map[netip.Prefix]bool{}
	for _, x := range a {
		for _, y := range b {
			var p netip.Prefix
			if x.Contains(y.Addr()) && x.Bits() <= y.Bits() {
				p = y
			} else if y.Contains(x.Addr()) && y.Bits() <= x.Bits() {
				p = x
			} else {
				continue
			}
			if !seen[p] {
				result = append(result, p.String())
				seen[p] = true
			}
		}
	}
	if len(result) == 0 {
		header = "direct"
	}
	out["ip-extractor"] = header
	out["ip-trust-list"] = strings.Join(result, ",")
	return nil
}
