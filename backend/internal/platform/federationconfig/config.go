package federationconfig

import (
	"net/url"
	"next-terminal/backend/internal/platform/fault"
	"strings"
)

type Provider struct {
	Managed          bool     `json:"settingsManaged"`
	Scopes           []string `json:"scopes"`
	Kind             string   `json:"kind"`
	AgentID          string   `json:"agentId"`
	AuthorizationURL string   `json:"authorizationUrl"`
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Issuer           string   `json:"issuer"`
	ClientID         string   `json:"clientId"`
	RedirectURI      string   `json:"redirectUri"`
	Enabled          bool     `json:"enabled"`
	BindingEnabled   bool     `json:"bindingEnabled"`
	HasSecret        bool     `json:"hasSecret"`
	Version          int64    `json:"version"`
}

func Validate(v Provider) error {
	if v.Kind != "wecom" {
		if e := ValidateScopes(v.Scopes); e != nil {
			return e
		}
	}
	if v.Kind != "" && v.Kind != "oidc" && v.Kind != "wecom" {
		return fault.Invalid
	}
	if v.Kind == "wecom" {
		u, e := url.Parse(v.AuthorizationURL)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(v.AuthorizationURL) > 2048 || v.AgentID == "" || len(v.AgentID) > 32 || len(v.ClientID) > 128 || strings.ContainsAny(v.ClientID, ":/ \r\n") {
			return fault.Invalid
		}
		for _, r := range v.AgentID {
			if r < '0' || r > '9' {
				return fault.Invalid
			}
		}
	}

	if strings.TrimSpace(v.Name) == "" || len(v.Name) > 100 || strings.TrimSpace(v.ClientID) == "" || len(v.ClientID) > 512 {
		return fault.Invalid
	}
	for _, value := range []string{v.Issuer, v.RedirectURI} {
		u, e := url.Parse(value)
		if e != nil || len(value) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(value, "*\r\n\\#") {
			return fault.Invalid
		}
	}
	return nil
}
func (p Provider) CallbackPath() string {
	if p.Kind == "wecom" {
		return "/api/wechat-work/callback"
	}
	return "/api/oidc/callback"
}

func (p Provider) RequestedScopes() []string {
	if p.Scopes == nil {
		return []string{"openid"}
	}
	return append([]string{}, p.Scopes...)
}
func ValidateScopes(scopes []string) error {
	if scopes == nil {
		return nil
	}
	if len(scopes) == 0 || len(scopes) > 32 {
		return fault.Invalid
	}
	seen := map[string]bool{}
	for _, scope := range scopes {
		if scope == "" || len(scope) > 128 || seen[scope] {
			return fault.Invalid
		}
		for _, r := range scope {
			if r < 0x21 || r > 0x7e || r == '"' || r == '\\' {
				return fault.Invalid
			}
		}
		seen[scope] = true
	}
	if !seen["openid"] {
		return fault.Invalid
	}
	return nil
}
