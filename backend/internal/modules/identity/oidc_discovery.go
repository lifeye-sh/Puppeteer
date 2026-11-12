package identity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type OIDCDiscovery struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	ResponseTypes         []string `json:"response_types_supported"`
	SigningAlgorithms     []string `json:"id_token_signing_alg_values_supported"`
	PKCEMethods           []string `json:"code_challenge_methods_supported"`
}

var ErrOIDCDiscovery = errors.New("OIDC metadata unavailable or incompatible")

// DiscoverOIDC performs no credential exchange and never sends client secrets.
// Redirects are rejected and the response is bounded before JSON parsing.
func DiscoverOIDC(ctx context.Context, issuer string, client *http.Client) (OIDCDiscovery, error) {
	var out OIDCDiscovery
	if e := ValidateOIDCProvider(OIDCProvider{Name: "discovery", ClientID: "probe", Issuer: issuer, RedirectURI: "https://callback.invalid/"}); e != nil {
		return out, ErrOIDCDiscovery
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	copyClient.Jar = nil
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(issuer, "/")+"/.well-known/openid-configuration", nil)
	if e != nil {
		return out, ErrOIDCDiscovery
	}
	request.Header.Set("Accept", "application/json")
	response, e := copyClient.Do(request)
	if e != nil {
		return out, ErrOIDCDiscovery
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return out, ErrOIDCDiscovery
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, 256*1024+1))
	if e != nil || len(data) > 256*1024 {
		return out, ErrOIDCDiscovery
	}
	if e = json.Unmarshal(data, &out); e != nil {
		return OIDCDiscovery{}, ErrOIDCDiscovery
	}
	if out.Issuer != issuer || !slices.Contains(out.ResponseTypes, "code") || !slices.Contains(out.SigningAlgorithms, "RS256") || !slices.Contains(out.PKCEMethods, "S256") {
		return OIDCDiscovery{}, ErrOIDCDiscovery
	}
	for _, endpoint := range []string{out.AuthorizationEndpoint, out.TokenEndpoint, out.JWKSURI} {
		u, e := url.Parse(endpoint)
		if e != nil || len(endpoint) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(endpoint, "\r\n\\#") {
			return OIDCDiscovery{}, ErrOIDCDiscovery
		}
	}
	return out, nil
}
func (s *Service) OIDCProvider(ctx context.Context, id string) (OIDCProvider, error) {
	var v OIDCProvider
	e := s.db.Row(ctx, `SELECT id,name,issuer,client_id,redirect_uri,enabled,binding_enabled,(secret_sealed IS NOT NULL OR configuration_secret_id IS NOT NULL),version,kind,agent_id,authorization_url,scopes,settings_managed FROM oidc_identity_providers WHERE id=$1`, id).Scan(&v.ID, &v.Name, &v.Issuer, &v.ClientID, &v.RedirectURI, &v.Enabled, &v.BindingEnabled, &v.HasSecret, &v.Version, &v.Kind, &v.AgentID, &v.AuthorizationURL, &v.Scopes, &v.Managed)
	return v, e
}
