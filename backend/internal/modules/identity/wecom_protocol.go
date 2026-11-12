package identity

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type FederationProtocol interface {
	Begin() (string, OIDCAuthorization)
	Exchange(context.Context, OIDCAuthorization, url.Values) (OIDCSubject, error)
}
type WeComProtocol struct {
	provider OIDCProvider
	secret   string
	client   *http.Client
}

func NewWeComProtocol(p OIDCProvider, secret string, client *http.Client) (*WeComProtocol, error) {
	if p.Kind != "wecom" || ValidateOIDCProvider(p) != nil || secret == "" || len(secret) > 8192 {
		return nil, ErrOIDCAuthorization
	}
	if client == nil {
		client = &http.Client{}
	}
	c := *client
	c.Timeout = 8 * time.Second
	c.Jar = nil
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.Transport = boundedOIDCTransport{base: base}
	return &WeComProtocol{p, secret, &c}, nil
}
func (p *WeComProtocol) Begin() (string, OIDCAuthorization) {
	c := OIDCAuthorization{State: NewToken(), ExpiresAt: time.Now().Add(5 * time.Minute)}
	u, _ := url.Parse(p.provider.AuthorizationURL)
	q := url.Values{"login_type": {"CorpApp"}, "appid": {p.provider.ClientID}, "agentid": {p.provider.AgentID}, "redirect_uri": {p.provider.RedirectURI}, "state": {c.State}}
	u.RawQuery = q.Encode()
	return u.String(), c
}
func (p *WeComProtocol) get(ctx context.Context, path string, q url.Values, out any) error {
	r, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(p.provider.Issuer, "/")+path+"?"+q.Encode(), nil)
	if e != nil {
		return ErrOIDCAuthorization
	}
	res, e := p.client.Do(r)
	if e != nil {
		return ErrOIDCAuthorization
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(out) != nil {
		return ErrOIDCAuthorization
	}
	return nil
}
func (p *WeComProtocol) accessToken(ctx context.Context) (string, error) {
	var token struct {
		Error   *int   `json:"errcode"`
		Token   string `json:"access_token"`
		Expires int    `json:"expires_in"`
	}
	if p.get(ctx, "/cgi-bin/gettoken", url.Values{"corpid": {p.provider.ClientID}, "corpsecret": {p.secret}}, &token) != nil || token.Error == nil || *token.Error != 0 || token.Token == "" || len(token.Token) > 8192 || token.Expires <= 0 {
		return "", ErrOIDCAuthorization
	}
	return token.Token, nil
}
func (p *WeComProtocol) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	_, e := p.accessToken(ctx)
	return e
}
func (p *WeComProtocol) Exchange(ctx context.Context, c OIDCAuthorization, q url.Values) (OIDCSubject, error) {
	var empty OIDCSubject
	if len(c.State) != 64 || !c.ExpiresAt.After(time.Now()) || c.ExpiresAt.After(time.Now().Add(5*time.Minute+time.Second)) || len(q["state"]) != 1 || len(q["code"]) != 1 || len(q["error"]) != 0 || q.Get("code") == "" || len(q.Get("code")) > 512 || subtle.ConstantTimeCompare([]byte(c.State), []byte(q.Get("state"))) != 1 {
		return empty, ErrOIDCAuthorization
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	token, e := p.accessToken(ctx)
	if e != nil {
		return empty, e
	}
	var user struct {
		Error  *int   `json:"errcode"`
		UserID string `json:"userid"`
	}
	if p.get(ctx, "/cgi-bin/auth/getuserinfo", url.Values{"access_token": {token}, "code": {q.Get("code")}}, &user) != nil || user.Error == nil || *user.Error != 0 || user.UserID == "" || len(user.UserID) > 128 || strings.ContainsAny(user.UserID, "\x00\r\n") {
		return empty, ErrOIDCAuthorization
	}
	subject := p.provider.ClientID + ":" + user.UserID
	if len(subject) > 255 {
		return empty, ErrOIDCAuthorization
	}
	return OIDCSubject{Issuer: p.provider.Issuer, Subject: subject}, nil
}
