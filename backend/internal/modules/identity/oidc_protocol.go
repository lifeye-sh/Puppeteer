package identity

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

var ErrOIDCAuthorization = errors.New("OIDC authorization failed")

// OIDCAuthorization is server-only challenge material, never a browser response.
// The application must persist it bound to the initiating browser/user, then
// atomically consume it BEFORE calling Exchange. A failed exchange is not retried.
type OIDCAuthorization struct {
	UserVersion int64     `json:"userVersion,omitempty"`
	State       string    `json:"state"`
	Nonce       string    `json:"nonce"`
	Verifier    string    `json:"verifier"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type OIDCSubject struct {
	Issuer  string
	Subject string
}

type OIDCProtocol struct {
	config   oauth2.Config
	metadata OIDCDiscovery
	client   *http.Client
}

// boundedOIDCTransport limits both token and JWKS responses. It preserves the
// caller's TLS validation (a trusted test CA may be injected by protocol tests).
type boundedOIDCTransport struct{ base http.RoundTripper }

func (t boundedOIDCTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" {
		return nil, ErrOIDCAuthorization
	}
	response, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, ErrOIDCAuthorization
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 256*1024+1))
	if err != nil || len(data) > 256*1024 {
		return nil, ErrOIDCAuthorization
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}

func NewOIDCProtocol(ctx context.Context, provider OIDCProvider, secret string, client *http.Client) (*OIDCProtocol, error) {
	if ValidateOIDCProvider(provider) != nil || secret == "" || len(secret) > 8192 {
		return nil, ErrOIDCAuthorization
	}
	if client == nil {
		client = &http.Client{}
	}
	c := *client
	c.Timeout = 8 * time.Second
	c.Jar = nil
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := c.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	c.Transport = boundedOIDCTransport{base: transport}
	metadata, err := DiscoverOIDC(ctx, provider.Issuer, &c)
	if err != nil {
		return nil, ErrOIDCAuthorization
	}
	return &OIDCProtocol{metadata: metadata, client: &c, config: oauth2.Config{
		ClientID: provider.ClientID, ClientSecret: secret, RedirectURL: provider.RedirectURI,
		Scopes:   provider.RequestedScopes(),
		Endpoint: oauth2.Endpoint{AuthURL: metadata.AuthorizationEndpoint, TokenURL: metadata.TokenEndpoint, AuthStyle: oauth2.AuthStyleInHeader},
	}}, nil
}

func (p *OIDCProtocol) Begin() (string, OIDCAuthorization) {
	c := OIDCAuthorization{State: NewToken(), Nonce: NewToken(), Verifier: oauth2.GenerateVerifier(), ExpiresAt: time.Now().Add(5 * time.Minute)}
	u := p.config.AuthCodeURL(c.State, oidc.Nonce(c.Nonce), oauth2.S256ChallengeOption(c.Verifier), oauth2.SetAuthURLParam("response_mode", "query"))
	return u, c
}

// Exchange accepts only a single code/state query response. It does not create
// users or infer identity from email; callers map the verified issuer+subject.
func (p *OIDCProtocol) Exchange(ctx context.Context, c OIDCAuthorization, query url.Values) (OIDCSubject, error) {
	var empty OIDCSubject
	if len(c.State) != 64 || len(c.Nonce) != 64 || len(c.Verifier) < 43 || len(c.Verifier) > 128 || !c.ExpiresAt.After(time.Now()) || c.ExpiresAt.After(time.Now().Add(5*time.Minute+time.Second)) {
		return empty, ErrOIDCAuthorization
	}
	if len(query["state"]) != 1 || len(query["code"]) != 1 || len(query["error"]) != 0 || len(query.Get("code")) == 0 || len(query.Get("code")) > 8192 || subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(c.State)) != 1 {
		return empty, ErrOIDCAuthorization
	}
	if values, ok := query["iss"]; ok && (len(values) != 1 || values[0] != p.metadata.Issuer) {
		return empty, ErrOIDCAuthorization
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ctx = oidc.ClientContext(ctx, p.client)
	token, err := p.config.Exchange(ctx, query.Get("code"), oauth2.VerifierOption(c.Verifier))
	if err != nil {
		return empty, ErrOIDCAuthorization
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || len(raw) > 64*1024 {
		return empty, ErrOIDCAuthorization
	}
	keys := oidc.NewRemoteKeySet(ctx, p.metadata.JWKSURI)
	verified, err := oidc.NewVerifier(p.metadata.Issuer, keys, &oidc.Config{ClientID: p.config.ClientID, SupportedSigningAlgs: []string{oidc.RS256}}).Verify(ctx, raw)
	if err != nil || verified.Issuer != p.metadata.Issuer || verified.Subject == "" || len(verified.Subject) > 255 || verified.IssuedAt.IsZero() || verified.IssuedAt.After(time.Now().Add(30*time.Second)) || subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(c.Nonce)) != 1 {
		return empty, ErrOIDCAuthorization
	}
	var claims struct {
		AuthorizedParty string `json:"azp"`
	}
	if verified.Claims(&claims) != nil || (claims.AuthorizedParty != "" && claims.AuthorizedParty != p.config.ClientID) || (len(verified.Audience) > 1 && claims.AuthorizedParty != p.config.ClientID) {
		return empty, ErrOIDCAuthorization
	}
	if verified.AccessTokenHash != "" && verified.VerifyAccessToken(token.AccessToken) != nil {
		return empty, ErrOIDCAuthorization
	}
	return OIDCSubject{Issuer: verified.Issuer, Subject: verified.Subject}, nil
}
