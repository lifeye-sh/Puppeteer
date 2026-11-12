package system

import (
	"context"
	"net"
	"net/url"
	"next-terminal/backend/internal/platform/fault"
	"regexp"
	"strconv"
	"strings"
)

var agentEndpointHost = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

func validateAgentEndpoint(raw string) error {
	if raw == "" {
		return nil
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || len(u.Hostname()) > 253 || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || u.Opaque != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return fault.Invalid
	}
	if net.ParseIP(u.Hostname()) == nil && !agentEndpointHost.MatchString(u.Hostname()) {
		return fault.Invalid
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return fault.Invalid
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return fault.Invalid
	}
	return nil
}

// A deployment default for new installations; existing devices retain the
// endpoint in their local configuration and are never remotely redirected.
func (s *Service) AgentEndpoint(ctx context.Context) (string, int64, error) {
	v, e := s.Configuration(ctx, 0)
	if e != nil {
		return "", 0, e
	}
	if e = ValidateConfiguration(v.Values); e != nil {
		return "", 0, e
	}
	return v.Values.String("agent-gateway-endpoint"), v.ID, nil
}
