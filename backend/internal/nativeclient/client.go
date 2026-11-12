// Package nativeclient implements the built-in public client's browser login.
// Access tokens live only in memory. Explicit persistent login stores refresh
// tokens in the system credential store, never a fallback credential file.
package nativeclient

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

const ClientID = "nt-native-v1"
const Version = "0.4.0-r14"

type Client struct {
	origin     string
	http       *http.Client
	deviceName string
}
type Asset struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Alias    string   `json:"alias"`
	Protocol string   `json:"protocol"`
	Revision int64    `json:"revision"`
	Entries  []string `json:"entries"`
}
type Token struct {
	Access   string `json:"access_token"`
	Refresh  string `json:"refresh_token,omitempty"`
	Type     string `json:"token_type"`
	Expires  int    `json:"expires_in"`
	Scope    string `json:"scope"`
	DeviceID string `json:"device_id"`
}

// LoginPersistent explicitly requests a rotating refresh token. Callers must
// save it in a system credential store, never a configuration or log file.
func (c *Client) LoginPersistent(ctx context.Context, open func(string) error, connect bool) (Token, error) {
	scope := "assets:read offline_access"
	if connect {
		scope = "assets:read sessions:connect offline_access"
	}
	return c.login(ctx, open, scope)
}

// Display name only; the server binds device login identity to the OAuth family.
func (c *Client) SetDeviceName(name string) { c.deviceName = name }

func New(origin string, transport http.RoundTripper) (*Client, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(origin, "#\\\r\n") {
		return nil, errors.New("server must be a fixed HTTPS origin")
	}
	return &Client{origin: strings.TrimSuffix(origin, "/"), http: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func random() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (c *Client) Login(ctx context.Context, open func(string) error) (Token, error) {
	return c.login(ctx, open, "assets:read")
}
func (c *Client) LoginForConnect(ctx context.Context, open func(string) error) (Token, error) {
	return c.login(ctx, open, "assets:read sessions:connect")
}
func (c *Client) login(ctx context.Context, open func(string) error, scope string) (Token, error) {
	var token Token
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return token, e
	}
	callback := "http://" + listener.Addr().String() + "/callback"
	state, verifier := random(), random()
	sum := sha256.Sum256([]byte(verifier))
	type outcome struct {
		code string
		err  error
	}
	result := make(chan outcome, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		q, e := url.ParseQuery(r.URL.RawQuery)
		valid := e == nil && r.Method == "GET" && r.Host == listener.Addr().String() && len(r.URL.RawQuery) < 4096 && subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) == 1
		for _, v := range q {
			if len(v) != 1 {
				valid = false
			}
		}
		if !valid {
			http.Error(w, "Invalid authorization response", 400)
			return
		}
		value := outcome{code: q.Get("code")}
		if q.Has("error") {
			value.err = errors.New("browser authorization was denied")
		} else if len(value.code) != 64 {
			http.Error(w, "Invalid authorization code", 400)
			return
		}
		select {
		case result <- value:
			io.WriteString(w, "Authorization received. You may close this window.")
		default:
			http.Error(w, "Authorization already received", 409)
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	q := url.Values{"client_id": {ClientID}, "redirect_uri": {callback}, "response_type": {"code"}, "scope": {scope}, "state": {state}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}}
	if e = open(c.origin + "/api/oidc/server/authorize?" + q.Encode()); e != nil {
		return token, e
	}
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	var value outcome
	select {
	case value = <-result:
	case <-ctx.Done():
		return token, ctx.Err()
	case <-timer.C:
		return token, errors.New("browser authorization timed out")
	}
	if value.err != nil {
		return token, value.err
	}
	stop, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = server.Shutdown(stop)
	cancel()
	body := url.Values{"client_id": {ClientID}, "grant_type": {"authorization_code"}, "code": {value.code}, "code_verifier": {verifier}, "redirect_uri": {callback}}
	body.Set("device_name", c.deviceName)
	body.Set("device_os", runtime.GOOS)
	body.Set("device_arch", runtime.GOARCH)
	body.Set("client_version", Version)
	if e = c.form(ctx, "token", body, &token); e != nil {
		return Token{}, e
	}
	if len(token.Access) != 64 || token.Type != "Bearer" || token.Expires < 1 || token.Expires > 300 || token.Scope != scope {
		return Token{}, errors.New("invalid token response")
	}
	return token, nil
}
func (c *Client) form(ctx context.Context, path string, v url.Values, out any) error {
	req, e := http.NewRequestWithContext(ctx, "POST", c.origin+"/api/oidc/server/"+path, strings.NewReader(v.Encode()))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req, out)
}
func (c *Client) do(req *http.Request, out any) error {
	res, e := c.http.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		var failure struct {
			Reason string `json:"reason"`
		}
		if json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&failure) == nil && len(failure.Reason) > 0 && len(failure.Reason) <= 80 {
			safe := true
			for _, r := range failure.Reason {
				if (r < 'a' || r > 'z') && r != '_' {
					safe = false
					break
				}
			}
			if safe {
				return fmt.Errorf("platform request rejected (HTTP %d; %s)", res.StatusCode, failure.Reason)
			}
		}
		return fmt.Errorf("platform request rejected (HTTP %d)", res.StatusCode)
	}
	if out == nil {
		return nil
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, 1048577))
	if e != nil {
		return e
	}
	if len(body) > 1048576 {
		return errors.New("platform response too large")
	}
	if json.Unmarshal(body, out) != nil {
		return errors.New("invalid platform response")
	}
	return nil
}
func (c *Client) Assets(ctx context.Context, token Token) ([]Asset, error) {
	items := []Asset{}
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 1000; page++ {
		req, e := http.NewRequestWithContext(ctx, "GET", c.origin+"/api/native/v1/assets?limit=100&cursor="+url.QueryEscape(cursor), nil)
		if e != nil {
			return nil, e
		}
		req.Header.Set("Authorization", "Bearer "+token.Access)
		var out struct {
			Items []Asset `json:"items"`
			Next  string  `json:"nextCursor"`
		}
		if e = c.do(req, &out); e != nil {
			return nil, e
		}
		if len(out.Items) > 100 {
			return nil, errors.New("invalid directory page")
		}
		items = append(items, out.Items...)
		if out.Next == "" {
			return items, nil
		}
		if len(out.Next) > 128 || seen[out.Next] {
			return nil, errors.New("invalid directory cursor")
		}
		seen[out.Next] = true
		cursor = out.Next
	}
	return nil, errors.New("directory exceeds client page limit")
}
func (c *Client) Logout(ctx context.Context, token Token) error {
	return c.form(ctx, "revoke", url.Values{"client_id": {ClientID}, "token": {token.Access}}, nil)
}
