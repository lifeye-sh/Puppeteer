package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/modules/website"
	"strings"
)

type trustedIPKey struct{}
type trustedIPAppliedKey struct{}

func (a *App) resolveAPIClient(r *http.Request) (*http.Request, error) {
	settings, e := a.system.SecurityConfiguration(r.Context())
	if e != nil {
		return nil, e
	}
	extractor := settings.Values.String("ip-extractor")
	forwarded := ""
	if extractor != "direct" && len(r.Header.Values(extractor)) == 1 {
		forwarded = r.Header.Get(extractor)
	}
	if extractor != "x-forwarded-for" && strings.Contains(forwarded, ",") {
		forwarded = ""
	}
	prefixes, e := system.TrustedProxyPrefixes(settings.Values.String("ip-trust-list"))
	if e != nil {
		return nil, e
	}
	trusted := []string{}
	for _, p := range prefixes {
		trusted = append(trusted, p.String())
	}
	ip, applied := website.ClientIPDetails(r.RemoteAddr, forwarded, trusted)
	ctx := context.WithValue(r.Context(), trustedIPKey{}, ip)
	return r.WithContext(context.WithValue(ctx, trustedIPAppliedKey{}, applied)), nil
}
