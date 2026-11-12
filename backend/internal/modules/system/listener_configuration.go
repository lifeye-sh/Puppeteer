package system

import (
	"github.com/google/uuid"
	"net"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"strings"
)

func validateListenerConfiguration(values ConfigurationValues) error {
	seen := map[string]bool{}
	for _, prefix := range []string{"http-server", "website-server", "native-rdp", "ssh-server", "db-proxy", "postgres-proxy"} {
		address := values.String(prefix + "-addr")
		if address == "" {
			continue
		}
		host, port, err := net.SplitHostPort(address)
		n, e := strconv.Atoi(port)
		if err != nil || e != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port || (host != "" && net.ParseIP(host) == nil) || strings.ContainsAny(address, " \t\r\n") {
			return fault.Invalid
		}
		if prefix == "http-server" || values.Bool(prefix+"-enabled") {
			if seen[address] {
				return fault.Invalid
			}
			seen[address] = true
		}
	}
	if address := values.String("native-rdp-public-address"); address != "" {
		host, port, err := net.SplitHostPort(address)
		n, e := strconv.Atoi(port)
		if err != nil || e != nil || host == "" || n < 1 || n > 65535 || strconv.Itoa(n) != port || strings.ContainsAny(host, " /\\\t\r\n@?#") {
			return fault.Invalid
		}
	}
	if id := values.String("db-proxy-certificate-id"); id != "" {
		u, e := uuid.Parse(id)
		if e != nil || u.String() != id {
			return fault.Invalid
		}
	}
	return nil
}
