package application

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

func (a *App) allowsBrowserOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	for _, allowed := range a.config.AllowedOrigins {
		if origin == allowed {
			return true
		}
	}
	if !a.config.AllowRequestOrigin {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	scheme := "http"
	if a.config.SecureCookies || r.TLS != nil {
		scheme = "https"
	}
	if u.Scheme != scheme {
		return false
	}
	// Only the actual HTTP Host is used. Arbitrary Forwarded/X-Forwarded-Host
	// headers must never grant an unrelated site access to cookie-authenticated APIs.
	target, err := url.Parse(scheme + "://" + r.Host)
	if err != nil || target.User != nil || target.Hostname() == "" || target.Path != "" || target.RawQuery != "" || target.Fragment != "" {
		return false
	}
	authority := func(v *url.URL) string {
		p := v.Port()
		if p == "" {
			if scheme == "https" {
				p = "443"
			} else {
				p = "80"
			}
		}
		return net.JoinHostPort(strings.ToLower(v.Hostname()), p)
	}
	return authority(u) == authority(target)
}
