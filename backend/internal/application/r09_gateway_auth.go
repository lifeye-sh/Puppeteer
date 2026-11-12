package application

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/jackc/pgx/v5"
	"html/template"
	"net/http"
	"net/url"
	"next-terminal/backend/internal/modules/authorization"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/website"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"time"
)

const websiteCookie = "__Host-nt-website"
const websiteStateCookie = "__Host-nt-login-state"

func (a *App) WebsiteTLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		ctx, cancel := context.WithTimeout(hello.Context(), 5*time.Second)
		defer cancel()
		domain, e := website.Domain(hello.ServerName)
		if e != nil {
			return nil, e
		}
		site, e := a.websites().Domain(ctx, domain)
		if e != nil {
			return nil, e
		}
		settings, e := a.websites().Settings(ctx)
		if e != nil {
			return nil, e
		}
		id := site.CertificateID
		if id == "" {
			id = settings.DefaultCertificateID
		}
		cert, e := a.certificates().TLS(ctx, id)
		if e != nil {
			return nil, e
		}
		if e = cert.Leaf.VerifyHostname(domain); e != nil {
			return nil, e
		}
		config := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}}
		if site.ClientCAID != "" {
			ca, e := a.certificates().Get(ctx, site.ClientCAID)
			if e != nil || ca.RevokedAt != nil {
				return nil, fault.Denied
			}
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM([]byte(ca.PEM)) {
				return nil, fault.Denied
			}
			config.ClientAuth = tls.RequireAndVerifyClientCert
			config.ClientCAs = roots
			config.VerifyConnection = func(state tls.ConnectionState) error {
				check, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				_, e := a.certificates().ClientIdentity(check, site.ClientCAID, state.PeerCertificates)
				return e
			}
		}
		return config, nil
	}}
}
func (a *App) websiteActor(r *http.Request, v website.Website, ip string) (user, temp string, err error) {
	ctx := r.Context()
	if v.ClientCAID != "" {
		if r.TLS == nil {
			return "", "", fault.Denied
		}
		user, err = a.certificates().ClientIdentity(ctx, v.ClientCAID, r.TLS.PeerCertificates)
		if err != nil {
			return
		}
		err = a.websiteGrant(ctx, user, v.ID)
		return
	}
	if v.Mode == "public" && !v.HasPassword {
		return "", "", nil
	}
	if v.Mode == "temporary_ip" {
		err = a.db.Row(ctx, `SELECT id::text,user_id::text FROM website_temporary_ips WHERE website_id=$1 AND ip=$2::inet AND revoked_at IS NULL AND expires_at>now() AND absolute_expires_at>now() ORDER BY expires_at DESC LIMIT 1`, v.ID, ip).Scan(&temp, &user)
		if err == nil {
			err = a.websiteGrant(ctx, user, v.ID)
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return
		}
		return "", "", fault.Denied
	}
	cookie, e := r.Cookie(websiteCookie)
	if e != nil {
		return "", "", fault.Denied
	}
	var version int64
	err = a.db.Row(ctx, `SELECT coalesce(user_id::text,''),coalesce(password_version,0) FROM website_sessions WHERE digest=$1 AND website_id=$2 AND expires_at>now()`, identity.Digest(cookie.Value), v.ID).Scan(&user, &version)
	if err != nil {
		return "", "", fault.Denied
	}
	if v.Mode == "public" {
		if version != v.Version {
			return "", "", fault.Denied
		}
		return "", "", nil
	}
	if user == "" {
		return "", "", fault.Denied
	}
	err = a.websiteGrant(ctx, user, v.ID)
	return
}
func (a *App) websitePolicy(r *http.Request, v website.Website, user, ip string) error {
	if !v.Enabled {
		return fault.Denied
	}
	facts := authorization.Facts{UserID: user, IP: ip, Action: "connect", ResourceIDs: []string{v.ID}}
	d, e := a.authz.EvaluateAccess(r.Context(), facts, v.Policy)
	if e != nil {
		return e
	}
	if !d.Allowed {
		return fault.Denied
	}
	if user != "" {
		return a.enforcePolicy(r.Context(), &identity.User{ID: user}, v.ID, ip, "connect", "")
	}
	d, e = a.authz.Evaluate(r.Context(), facts)
	if e != nil {
		return e
	}
	if !d.Allowed {
		return fault.Denied
	}
	return nil
}
func websiteSetCookie(w http.ResponseWriter, name, value string, seconds int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: seconds})
}
func (a *App) websiteRedirect(w http.ResponseWriter, r *http.Request, v website.Website, settings website.Settings) {
	if r.Method != "GET" || strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "Website authentication required", 401)
		return
	}
	state := identity.NewToken()
	websiteSetCookie(w, websiteStateCookie, state, 300)
	target := settings.ConsoleURL + "/website-login?" + url.Values{"websiteId": {v.ID}, "state": {state}, "return": {r.URL.RequestURI()}}.Encode()
	http.Redirect(w, r, target, http.StatusFound)
}
func (a *App) websiteCallback(w http.ResponseWriter, r *http.Request, v website.Website, ip string) error {
	if r.Method != "GET" {
		return fault.Denied
	}
	cookie, e := r.Cookie(websiteStateCookie)
	if e != nil || len(cookie.Value) != 64 {
		return fault.Denied
	}
	code := r.URL.Query().Get("code")
	if len(code) != 64 {
		return fault.Denied
	}
	session := identity.NewToken()
	var user, returnPath string
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		e := a.db.Row(ctx, `UPDATE website_login_codes SET consumed_at=now() WHERE digest=$1 AND website_id=$2 AND state_digest=$3 AND expires_at>now() AND consumed_at IS NULL RETURNING user_id::text,return_path`, identity.Digest(code), v.ID, identity.Digest(cookie.Value)).Scan(&user, &returnPath)
		if e != nil {
			return fault.Denied
		}
		if !website.SafePath(returnPath) {
			return fault.Invalid
		}
		if e = a.websiteGrant(ctx, user, v.ID); e != nil {
			return e
		}
		if e = a.websitePolicy(r.WithContext(ctx), v, user, ip); e != nil {
			return e
		}
		if v.Mode == "temporary_ip" {
			_, e = a.db.Exec(ctx, `INSERT INTO website_temporary_ips(website_id,user_id,ip,expires_at,absolute_expires_at) VALUES($1,$2,$3::inet,now()+make_interval(mins=>$4),now()+make_interval(mins=>$5))`, v.ID, user, ip, v.TempTTL, v.TempMax)
		} else {
			_, e = a.db.Exec(ctx, `INSERT INTO website_sessions(digest,website_id,user_id,expires_at) VALUES($1,$2,$3,now()+interval '8 hours')`, identity.Digest(session), v.ID, user)
		}
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, user, "website.login_completed", "website", ip, map[string]string{"id": v.ID})
	})
	if e != nil {
		return e
	}
	websiteSetCookie(w, websiteStateCookie, "", -1)
	if v.Mode != "temporary_ip" {
		websiteSetCookie(w, websiteCookie, session, 28800)
	}
	http.Redirect(w, r, returnPath, http.StatusSeeOther)
	return nil
}

var passwordPage = template.Must(template.New("password").Parse(`<!doctype html><html lang="zh"><meta charset="utf-8"><title>网站访问验证</title><body><h1>{{.Name}}</h1><form method="post" action="/_nt/password"><input type="hidden" name="state" value="{{.State}}"><label>访问口令 <input name="password" type="password" required maxlength="256"></label><button>访问网站</button></form></body></html>`))

func (a *App) websitePassword(w http.ResponseWriter, r *http.Request, v website.Website) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'")
	if r.Method == "GET" {
		state := identity.NewToken()
		websiteSetCookie(w, websiteStateCookie, state, 300)
		return passwordPage.Execute(w, map[string]string{"Name": v.Name, "State": state})
	}
	if r.Method != "POST" || r.Header.Get("Origin") != a.websiteURL(v) {
		return fault.Denied
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if e := r.ParseForm(); e != nil {
		return fault.Invalid
	}
	cookie, e := r.Cookie(websiteStateCookie)
	if e != nil || cookie.Value != r.Form.Get("state") || len(cookie.Value) != 64 {
		return fault.Denied
	}
	if !a.allowAttempt("website-password:" + v.ID + ":" + website.ClientIP(r.RemoteAddr, "", nil)) {
		return fault.Locked
	}
	var hash string
	if e = a.db.Row(r.Context(), "SELECT public_password_hash FROM websites WHERE id=$1", v.ID).Scan(&hash); e != nil {
		return e
	}
	release, e := a.hashPermit(r)
	if e != nil {
		return e
	}
	ok := identity.CheckPassword(hash, r.Form.Get("password"))
	release()
	if !ok {
		return fault.Proof
	}
	session := identity.NewToken()
	if _, e = a.db.Exec(r.Context(), `INSERT INTO website_sessions(digest,website_id,password_version,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')`, identity.Digest(session), v.ID, v.Version); e != nil {
		return e
	}
	websiteSetCookie(w, websiteCookie, session, 3600)
	websiteSetCookie(w, websiteStateCookie, "", -1)
	http.Redirect(w, r, v.EntryPath, http.StatusSeeOther)
	return nil
}
