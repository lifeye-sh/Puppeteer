package modelendpoint

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

var ErrInvalid = errors.New("invalid model endpoint")

func Parse(raw string, private bool) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 2048 || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, "\r\n") || strings.Contains(u.Host, "%") {
		return nil, ErrInvalid
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && private) {
		return nil, ErrInvalid
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return nil, ErrInvalid
		}
	}
	return u, nil
}

// Proxy endpoints have no path or embedded credential. SOCKS destinations are
// resolved and pinned by the caller, including the socks5h spelling.
func ParseProxy(raw string, private bool) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil {
		return nil, ErrInvalid
	}
	if u.Scheme == "socks5" || u.Scheme == "socks5h" {
		if u.Port() == "" {
			return nil, ErrInvalid
		}
		copy := *u
		copy.Scheme = "https"
		if _, e = Parse(copy.String(), private); e != nil {
			return nil, e
		}
	} else if _, e = Parse(raw, private); e != nil {
		return nil, e
	}
	if len(raw) > 2048 || (u.Path != "" && u.Path != "/") || u.ForceQuery || u.Opaque != "" {
		return nil, ErrInvalid
	}
	return u, nil
}
