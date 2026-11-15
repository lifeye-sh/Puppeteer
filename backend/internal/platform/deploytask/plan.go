package deploytask

import (
	"errors"
	"net/netip"
	"regexp"
)

// Only these entries can be managed. Management HTTP and native RDP require
// separate capabilities and cannot be requested through this contract.
func ContainerPort(protocol string) (int, bool) {
	ports := map[string]int{"mysql": 13306, "postgres": 15432, "website": 18443, "ssh": 2022, "management": 8089}
	port, ok := ports[protocol]
	return port, ok
}
func Validate(r Request) error {
	if specialized(r.Protocol) {
		return validateSpecialized(r)
	}
	if r.AllowCIDRs != "" || r.AdminIP != "" || r.Repository != "" || r.TrustRoot != "" {
		return errors.New("specialized options are not accepted by this operation")
	}
	if r.Protocol == "graphics" || r.Protocol == "rdp-drive" || r.Protocol == "mp4" {
		if r.SiteID != "" || r.SiteVersion != 0 || r.SiteEnabled || r.Bind != "" || r.Port != 0 {
			return errors.New("internal component and accepts no host ports or website target")
		}
		return nil
	}
	if r.SiteID != "" && (r.Protocol != "website" || r.SiteVersion < 1 || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(r.SiteID)) {
		return errors.New("invalid website deployment request")
	}
	if _, ok := ContainerPort(r.Protocol); !ok {
		return errors.New("unsupported deployment protocol")
	}
	if r.Protocol == "management" && (!r.Enabled || r.Bind != "0.0.0.0") {
		return errors.New("management must remain enabled on container address 0.0.0.0")
	}
	// Disables identify an owned mapping by protocol; they need no caller ports.
	if !r.Enabled {
		return nil
	}
	ip, err := netip.ParseAddr(r.Bind)
	if err != nil || !ip.Is4() || ip.IsMulticast() {
		return errors.New("bind must be a unicast or wildcard IPv4 address")
	}
	if r.Port < 1 || r.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	return nil
}

func (r Request) IsDisable() bool { return !r.Enabled || (r.SiteID != "" && !r.SiteEnabled) }
