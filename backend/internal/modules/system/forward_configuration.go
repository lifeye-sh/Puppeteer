package system

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/netip"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const ForwardEnabledKey = "ssh-server-port-forwarding-enabled"
const ForwardRulesKey = "ssh-server-port-forwarding-host-port"

type forwardProofKey struct{}

func WithForwardConfigurationProof(ctx context.Context) context.Context {
	return context.WithValue(ctx, forwardProofKey{}, true)
}
func hasForwardConfigurationProof(ctx context.Context) bool {
	v, _ := ctx.Value(forwardProofKey{}).(bool)
	return v
}
func IsForwardConfigurationKey(k string) bool { return k == ForwardEnabledKey || k == ForwardRulesKey }

type NativeForwardRule struct {
	AssetID   string   `json:"assetId"`
	Host      string   `json:"host"`
	Port      int      `json:"port"`
	Addresses []string `json:"addresses"`
}
type forwardDestination struct {
	AssetID string `json:"assetId"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
}

var forwardDNSLabel = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

func forwardHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !forwardDNSLabel.MatchString(label) {
			return false
		}
	}
	return true
}
func ForwardAddress(ip string) bool {
	v, e := netip.ParseAddr(ip)
	v = v.Unmap()
	return e == nil && v.Zone() == "" && !v.IsUnspecified() && !v.IsMulticast() && !v.IsLinkLocalUnicast() && !v.IsLinkLocalMulticast()
}

// The public configuration contains no DNS pins. Only the server resolves pins.
func EncodeForwardRules(rules []NativeForwardRule) string {
	out := make([]forwardDestination, 0, len(rules))
	for _, r := range rules {
		out = append(out, forwardDestination{r.AssetID, r.Host, r.Port})
	}
	raw, _ := json.Marshal(out)
	return string(raw)
}
func ParseForwardRules(raw string) ([]NativeForwardRule, error) {
	var in []forwardDestination
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || in == nil || len(in) > 100 {
		return nil, fault.Invalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, fault.Invalid
	}
	out := make([]NativeForwardRule, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		v.Host = strings.ToLower(v.Host)
		if ip, e := netip.ParseAddr(v.Host); e == nil {
			v.Host = ip.Unmap().String()
		}
		if !resource.ValidID(v.AssetID) || !forwardHost(v.Host) || v.Port < 1 || v.Port > 65535 {
			return nil, fault.Invalid
		}
		key := v.AssetID + "/" + net.JoinHostPort(v.Host, strconv.Itoa(v.Port))
		if seen[key] {
			return nil, fault.Invalid
		}
		seen[key] = true
		out = append(out, NativeForwardRule{AssetID: v.AssetID, Host: v.Host, Port: v.Port, Addresses: []string{}})
	}
	return out, nil
}
func validateForwardConfiguration(v ConfigurationValues) error {
	rules, e := ParseForwardRules(v.String(ForwardRulesKey))
	if e != nil || (v.Bool(ForwardEnabledKey) && len(rules) == 0) {
		return fault.Invalid
	}
	return nil
}
func (s *Service) applyForwardConfiguration(ctx context.Context, v ConfigurationValues) error {
	rules, e := ParseForwardRules(v.String(ForwardRulesKey))
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Emergency disable is independent of target availability or deleted assets.
	if v.Bool(ForwardEnabledKey) {
		for i := range rules {
			r := &rules[i]
			var valid bool
			if e = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM assets a JOIN resource_registry r ON r.id=a.id WHERE a.id=$1 AND a.protocol='ssh' AND r.deleted_at IS NULL)`, r.AssetID).Scan(&valid); e != nil {
				return e
			}
			if !valid {
				return fault.Invalid
			}
			ips := []string{r.Host}
			if net.ParseIP(r.Host) == nil {
				c, stop := context.WithTimeout(ctx, 3*time.Second)
				ips, e = net.DefaultResolver.LookupHost(c, r.Host)
				stop()
				if e != nil {
					return fault.Invalid
				}
			}
			pins := map[string]bool{}
			for _, ip := range ips {
				if !ForwardAddress(ip) {
					return fault.Invalid
				}
				addr, _ := netip.ParseAddr(ip)
				pins[addr.Unmap().String()] = true
			}
			if len(pins) == 0 || len(pins) > 16 {
				return fault.Invalid
			}
			for ip := range pins {
				r.Addresses = append(r.Addresses, ip)
			}
			sort.Strings(r.Addresses)
		}
	}
	raw, e := json.Marshal(rules)
	if e != nil {
		return e
	}
	// Re-pinned IPs also require ACK, even when host/port text is unchanged.
	// This temporary floor is committed only with the new immutable revision.
	if _, e = s.db.Exec(ctx, `UPDATE configuration_security SET enforced=jsonb_set(enforced,'{ssh-server-port-forwarding-enabled}','false'::jsonb) WHERE singleton=1`); e != nil {
		return e
	}
	// Enabled is set from the enforcement overlay later in the same transaction.
	_, e = s.db.Exec(ctx, `UPDATE native_forward_settings SET rules=$1,version=version+1 WHERE singleton=1`, raw)
	return e
}
func (s *Service) enforceForwardConfiguration(ctx context.Context) error {
	_, e := s.db.Exec(ctx, `UPDATE native_forward_settings f SET enabled=(c.enforced->>'ssh-server-port-forwarding-enabled')::boolean,version=f.version+1 FROM configuration_security c WHERE f.singleton=1 AND c.singleton=1 AND f.enabled IS DISTINCT FROM (c.enforced->>'ssh-server-port-forwarding-enabled')::boolean`)
	return e
}
