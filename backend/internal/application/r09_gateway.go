package application

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/website"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"strings"
	"time"
)

func (a *App) WebsiteHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.enterRequest() {
			http.Error(w, "Maintenance", 503)
			return
		}
		defer a.requests.RUnlock()
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		host := r.Host
		if h, _, e := net.SplitHostPort(host); e == nil {
			host = h
		}
		domain, e := website.Domain(host)
		if e != nil || r.TLS == nil || !strings.EqualFold(r.TLS.ServerName, domain) {
			http.Error(w, "Unknown website", 421)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Hour)
		defer cancel()
		r = r.WithContext(ctx)
		site, e := a.websites().Domain(ctx, domain)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		settings, e := a.websites().Settings(ctx)
		if e != nil {
			http.Error(w, "Website unavailable", 503)
			return
		}
		ip := website.ClientIP(r.RemoteAddr, r.Header.Get("X-Forwarded-For"), settings.WebsiteTrustedProxies)
		if r.URL.Path == "/_nt/callback" {
			if e = a.websiteCallback(w, r, site, ip); e != nil {
				a.websiteFailure(w, r, site, ip, "", 403, "login_callback_denied")
			}
			return
		}
		if r.URL.Path == "/_nt/password" {
			if site.Mode != "public" || !site.HasPassword {
				http.NotFound(w, r)
				return
			}
			if e = a.websitePolicy(r, site, "", ip); e != nil {
				a.websiteFailure(w, r, site, ip, "", 403, "policy_denied")
				return
			}
			if e = a.websitePassword(w, r, site); e != nil {
				a.websiteFailure(w, r, site, ip, "", 403, "password_denied")
			}
			return
		}
		if strings.HasPrefix(r.URL.Path, "/_nt/") {
			http.NotFound(w, r)
			return
		}
		user, temp, e := a.websiteActor(r, site, ip)
		if e != nil {
			if site.ClientCAID != "" {
				a.websiteFailure(w, r, site, ip, "", 403, "client_certificate_denied")
				return
			}
			if site.Mode == "public" && site.HasPassword {
				http.Redirect(w, r, "/_nt/password", http.StatusSeeOther)
			} else {
				a.websiteRedirect(w, r, site, settings)
			}
			return
		}
		if e = a.websitePolicy(r, site, user, ip); e != nil {
			a.websiteFailure(w, r, site, ip, user, 403, "policy_denied")
			return
		}
		upgrade := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		if upgrade && r.Header.Get("Origin") != "" && r.Header.Get("Origin") != a.websiteURL(site) {
			a.websiteFailure(w, r, site, ip, user, 403, "websocket_origin_denied")
			return
		}
		// Long responses and WebSockets retain no authority after a policy, grant or certificate change.
		done := make(chan struct{})
		defer close(done)
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				case <-ticker.C:
					current, e := a.websites().Get(ctx, site.ID)
					if e != nil || !current.Enabled || current.Version != site.Version {
						cancel()
						return
					}
					if _, _, e = a.websiteActor(r, current, ip); e != nil {
						cancel()
						return
					}
					if e = a.websitePolicy(r, current, user, ip); e != nil {
						cancel()
						return
					}
				}
			}
		}()
		if e = a.proxyWebsite(w, r, site, ip, user, temp); e != nil {
			a.websiteFailure(w, r, site, ip, user, 502, "upstream_unavailable")
		}
	})
}
func (a *App) websiteLog(ctx context.Context, v website.Website, user, ip, method, path string, status int, reason string, duration time.Duration) error {
	if !v.Audit {
		return nil
	}
	_, e := a.db.Exec(ctx, `INSERT INTO website_access_logs(website_id,user_id,client_ip,method,path,status,reason,duration_ms) VALUES($1,nullif($2,'')::uuid,$3,$4,$5,$6,$7,$8)`, v.ID, user, ip, method, path, status, reason, duration.Milliseconds())
	return e
}
func (a *App) websiteFailure(w http.ResponseWriter, r *http.Request, v website.Website, ip, user string, status int, reason string) {
	_ = a.websiteLog(r.Context(), v, user, ip, r.Method, r.URL.Path, status, reason, 0)
	http.Error(w, "Website access failed: "+reason, status)
}
func reservedCookie(name string) bool {
	return name == CookieName || strings.HasPrefix(name, "__Host-nt-") || name == "X-Auth-Token"
}
func (a *App) proxyWebsite(w http.ResponseWriter, r *http.Request, v website.Website, ip, user, temp string) error {
	target, e := url.Parse(v.Upstream)
	if e != nil {
		return e
	}
	started := time.Now()
	authorize := func(ctx context.Context) error {
		current, e := a.websites().Get(ctx, v.ID)
		if e != nil {
			return e
		}
		if !current.Enabled || current.Version != v.Version {
			return fault.Denied
		}
		_, _, e = a.websiteActor(r.WithContext(ctx), current, ip)
		return e
	}
	var dial connectivity.Dialer = connectivity.DialTCP
	lease := "website-" + identity.NewToken()[:24]
	switch {
	case v.AgentID != "":
		dial = func(ctx context.Context, h string, p int) (net.Conn, error) {
			return a.connectivity.DialAgent(r.Context(), ctx, v.AgentID, lease, h, p, authorize)
		}
	case v.JumpID != "":
		dial = a.jumpDialer(r.Context(), v.JumpID, user, lease)
	case v.GatewayGroupID != "":
		dial = a.groupDialer(r.Context(), v.GatewayGroupID, user, lease, authorize)
	case v.ProxyID != "":
		dial = a.proxyDialer(r.Context(), v.ProxyID, user, lease)
	}
	roots, e := x509.SystemCertPool()
	if e != nil {
		roots = x509.NewCertPool()
	}
	if v.UpstreamCA != "" && !roots.AppendCertsFromPEM([]byte(v.UpstreamCA)) {
		return fault.Invalid
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: target.Hostname()}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, MaxResponseHeaderBytes: 64 * 1024, DisableCompression: true, DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		if host != target.Hostname() {
			return nil, fault.Denied
		}
		number, e := strconv.Atoi(port)
		if e != nil {
			return nil, e
		}
		c, stop := context.WithTimeout(ctx, 15*time.Second)
		defer stop()
		return dial(c, host, number)
	}}
	defer transport.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{Transport: transport, FlushInterval: -1, Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		pr.Out.Host = target.Host
		for _, name := range []string{"Authorization", "Proxy-Authorization", "X-Auth-Token", "X-Internal-Request-ID", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"} {
			pr.Out.Header.Del(name)
		}
		pr.Out.Header.Del("Cookie")
		for _, cookie := range pr.In.Cookies() {
			if !reservedCookie(cookie.Name) {
				pr.Out.AddCookie(cookie)
			}
		}
		pr.Out.Header.Set("X-Forwarded-For", ip)
		pr.Out.Header.Set("X-Forwarded-Proto", "https")
		pr.Out.Header.Set("X-Forwarded-Host", v.Domain)
		if pr.Out.Header.Get("Origin") != "" {
			pr.Out.Header.Set("Origin", target.Scheme+"://"+target.Host)
		}
		for key, value := range v.RequestHeaders {
			pr.Out.Header.Set(key, value)
		}
	}, ModifyResponse: func(res *http.Response) error {
		if e := authorize(r.Context()); e != nil {
			return e
		}
		// The upstream never controls platform authentication cookies.
		cookies := res.Cookies()
		res.Header.Del("Set-Cookie")
		for _, cookie := range cookies {
			if reservedCookie(cookie.Name) {
				continue
			}
			cookie.Domain = ""
			cookie.Secure = true
			res.Header.Add("Set-Cookie", cookie.String())
		}
		if location := res.Header.Get("Location"); location != "" {
			u, e := url.Parse(location)
			if e == nil && u.Host == target.Host {
				u.Scheme = "https"
				public, _ := url.Parse(a.websiteURL(v))
				u.Host = public.Host
				res.Header.Set("Location", u.String())
			}
		}
		for key, value := range v.ResponseHeaders {
			res.Header.Set(key, value)
		}
		if res.StatusCode >= 200 && res.StatusCode < 400 && temp != "" && v.TempAutoRenew {
			if _, e := a.db.Exec(r.Context(), `UPDATE website_temporary_ips SET expires_at=least(absolute_expires_at,now()+make_interval(mins=>$2)) WHERE id=$1 AND revoked_at IS NULL AND expires_at>now() AND absolute_expires_at>now()`, temp, v.TempTTL); e != nil {
				return e
			}
		}
		if res.StatusCode == 101 {
			body := res.Body
			context.AfterFunc(r.Context(), func() { _ = body.Close() })
		}
		if e := rewriteWebsiteResponse(res, v.Rewrites); e != nil {
			return e
		}
		return a.websiteLog(r.Context(), v, user, ip, r.Method, r.URL.Path, res.StatusCode, "", time.Since(started))
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, _ error) {
		a.websiteFailure(w, r, v, ip, user, 502, "upstream_unavailable")
	}}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	proxy.ServeHTTP(w, r)
	return nil
}
func rewriteWebsiteResponse(res *http.Response, rules []website.Rewrite) error {
	if len(rules) == 0 || res.StatusCode == 101 || res.Request != nil && res.Request.Method == "HEAD" {
		return nil
	}
	media, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if media != "text/html" && media != "text/css" && media != "text/javascript" && media != "application/javascript" && media != "application/json" && media != "text/plain" {
		return nil
	}
	encoding := res.Header.Get("Content-Encoding")
	if encoding != "" && encoding != "gzip" {
		return nil
	}
	const limit = 1 << 20
	if res.ContentLength > limit {
		return nil
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if e != nil {
		return e
	}
	if len(raw) > limit {
		res.Body = &readerCloser{Reader: io.MultiReader(bytes.NewReader(raw), res.Body), Closer: res.Body}
		return nil
	}
	res.Body.Close()
	res.Body = io.NopCloser(bytes.NewReader(raw))
	content := raw
	if encoding == "gzip" {
		reader, e := gzip.NewReader(bytes.NewReader(raw))
		if e != nil {
			return e
		}
		content, e = io.ReadAll(io.LimitReader(reader, limit+1))
		reader.Close()
		if e != nil {
			return e
		}
		if len(content) > limit {
			return nil
		}
	}
	for _, rule := range rules {
		size := int64(len(content)) + int64(bytes.Count(content, []byte(rule.Find)))*int64(len(rule.Replace)-len(rule.Find))
		if size > 4*limit {
			return errors.New("rewritten response exceeds limit")
		}
		content = bytes.ReplaceAll(content, []byte(rule.Find), []byte(rule.Replace))
	}
	res.Header.Del("Content-Encoding")
	res.Header.Del("ETag")
	res.Header.Del("Content-MD5")
	res.Header.Del("Digest")
	res.Header.Del("Accept-Ranges")
	res.Header.Set("Content-Length", strconv.Itoa(len(content)))
	res.ContentLength = int64(len(content))
	res.Body = io.NopCloser(bytes.NewReader(content))
	return nil
}

type readerCloser struct {
	io.Reader
	io.Closer
}
