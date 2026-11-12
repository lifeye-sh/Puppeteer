package application

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"golang.org/x/crypto/ssh"
	"net"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/listener"
	"strings"
)

func (a *App) requestedListenerAddress(v system.ConfigurationValues, name string) string {
	prefix, inherited := "ssh-server", a.config.NativeSSHAddr
	if name == "mysql" {
		prefix, inherited = "db-proxy", a.config.DatabaseProxyAddr
	}
	if name == "postgres" {
		prefix, inherited = "postgres-proxy", a.config.PostgresProxyAddr
	}
	if !v.Bool(prefix + "-enabled") {
		return ""
	}
	if address := v.String(prefix + "-addr"); address != "" {
		return address
	}
	return inherited
}

// The published address is taken from the successfully switched generation,
// never from a requested revision whose port or certificate failed preparation.
func (a *App) effectiveListenerAddress(name string) string {
	a.configurationMu.Lock()
	defer a.configurationMu.Unlock()
	if a.listenerManager != nil {
		for _, state := range a.listenerManager.States() {
			if state.Name == name {
				return state.Address
			}
		}
		return ""
	}
	// Used by isolated protocol fixtures that explicitly run their own listener.
	switch name {
	case "ssh":
		return a.config.NativeSSHAddr
	case "mysql":
		return a.config.DatabaseProxyAddr
	case "postgres":
		return a.config.PostgresProxyAddr
	}
	return ""
}

func (a *App) prepareListeners(ctx context.Context, v system.Revision) (*listener.Prepared, string, error) {
	specs := []listener.Spec{}
	if rdp, e := a.managedRDPSpec(v.Values); e != nil {
		return nil, "", e
	} else if rdp != nil {
		specs = append(specs, *rdp)
	}
	sshAddress := a.requestedListenerAddress(v.Values, "ssh")
	if sshAddress != "" {
		signer, err := a.configuredGatewayKey(ctx, v.Values)
		if err != nil {
			return nil, "", err
		}
		specs = append(specs, listener.Spec{Name: "ssh", Address: sshAddress, Identity: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), Handle: func(c context.Context, raw net.Conn) {
			if _, err := a.appliedConfiguration(c); err != nil {
				raw.Close()
				return
			}
			a.nativeGatewayConnection(c, raw, signer)
		}})
	}
	mysqlAddress, pgAddress := a.requestedListenerAddress(v.Values, "mysql"), a.requestedListenerAddress(v.Values, "postgres")
	certificateID := ""
	if mysqlAddress != "" || pgAddress != "" {
		var tlsConfig *tls.Config
		var err error
		certificateID = v.Values.String("db-proxy-certificate-id")
		if certificateID == "" {
			tlsConfig, certificateID, err = a.defaultDatabaseProxyTLS(ctx)
		} else {
			metadata, e := a.certificates().Get(ctx, certificateID)
			if e != nil {
				return nil, "", e
			}
			if metadata.Kind != "server" {
				return nil, "", fault.Invalid
			}
			certificate, e := a.certificates().TLS(ctx, certificateID)
			if e != nil {
				return nil, "", e
			}
			if certificate.Leaf.VerifyHostname(a.config.DatabaseProxyName) != nil {
				return nil, "", fault.Invalid
			}
			tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
		}
		if err != nil {
			return nil, "", err
		}
		identity := fmt.Sprintf("%x", sha256.Sum256(tlsConfig.Certificates[0].Certificate[0]))
		if mysqlAddress != "" {
			specs = append(specs, listener.Spec{Name: "mysql", Address: mysqlAddress, Identity: identity, Handle: func(c context.Context, raw net.Conn) {
				if _, err := a.appliedConfiguration(c); err != nil {
					raw.Close()
					return
				}
				a.databaseConnection(c, raw, tlsConfig)
			}})
		}
		if pgAddress != "" {
			protocol := a.postgresProtocol(tlsConfig)
			specs = append(specs, listener.Spec{Name: "postgres", Address: pgAddress, Identity: identity, Handle: func(c context.Context, raw net.Conn) {
				host, _, _ := net.SplitHostPort(raw.RemoteAddr().String())
				if !a.allowAttempt("postgres-connect:" + host) {
					raw.Close()
					return
				}
				if _, err := a.appliedConfiguration(c); err != nil {
					raw.Close()
					return
				}
				if !a.enterRequest() {
					raw.Close()
					return
				}
				defer a.requests.RUnlock()
				protocol.ServeConn(c, raw)
			}})
		}
	}
	p, err := a.listenerManager.Prepare(ctx, specs)
	return p, certificateID, err
}

func (a *App) refreshListenerReadiness(v system.ConfigurationValues) {
	ssh, mysql, pg := false, false, false
	var rdpAddress *string
	for _, s := range a.listenerManager.States() {
		switch s.Name {
		case "rdp":
			endpoint := v.String("native-rdp-public-address")
			if endpoint == "" && a.managedRDP != nil {
				endpoint = a.managedRDP.Advertised
			}
			if endpoint == "" {
				endpoint = s.Address
			}
			rdpAddress = &endpoint
		case "ssh":
			ssh = true
		case "mysql":
			mysql = true
		case "postgres":
			pg = true
		}
	}
	a.nativeSSHReady.Store(ssh)
	a.databaseProxyReady.Store(mysql)
	a.postgresProxyReady.Store(pg)
	if a.managedRDP != nil {
		a.nativeRDPAddress.Store(rdpAddress)
	}
}
