// Package mysqlproxy contains the TLS-only MySQL protocol boundary.
package mysqlproxy

import (
	"context"
	"crypto/tls"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/server"
	"time"
)

// FullAuth deliberately has no fast-auth cache and never uses a target password.
// Verify receives the platform wire identity and transient dedicated password.
type FullAuth struct {
	Verify func(context.Context, string, string) error
}

func (p *FullAuth) Validate(plugin string) bool { return plugin == mysql.AUTH_CACHING_SHA2_PASSWORD }
func (p *FullAuth) Authenticate(c *server.Conn, plugin string, _ []byte) error {
	if !p.Validate(plugin) || p.Verify == nil {
		return server.ErrAccessDenied
	}
	conn, ok := c.Conn.Conn.(*tls.Conn)
	if !ok {
		return server.ErrAccessDenied
	}
	state := conn.ConnectionState()
	if !state.HandshakeComplete || state.Version < tls.VersionTLS12 {
		return server.ErrAccessDenied
	}
	// Header space is required by packet.Conn.WritePacket. 0x01/0x04 requests
	// caching_sha2_password full authentication over the established TLS channel.
	if e := c.WritePacket([]byte{0, 0, 0, 0, 1, 4}); e != nil {
		return e
	}
	sequence := c.Conn.Sequence
	password, e := readPacket(conn, &sequence, 44)
	c.Conn.Sequence = sequence
	if e != nil {
		return e
	}
	defer clear(password)
	if len(password) != 44 || password[len(password)-1] != 0 {
		return server.ErrAccessDenied
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e = p.Verify(ctx, c.GetUser(), string(password[:len(password)-1])); e != nil {
		return server.ErrAccessDenied
	}
	return nil
}

type IdentityHandler struct {
	Ready   func(*server.Conn) error
	Failure func(*server.Conn, error)
}

func (*IdentityHandler) GetCredential(string) (server.Credential, bool, error) {
	// The protocol library rejects an empty credential slice before invoking the
	// custom provider. This sentinel is never compared with client credentials;
	// FullAuth is the sole verifier and always requests full TLS authentication.
	return server.Credential{Passwords: []string{"!external-verifier-only!"}, AuthPluginName: mysql.AUTH_CACHING_SHA2_PASSWORD}, true, nil
}
func (h *IdentityHandler) OnAuthSuccess(c *server.Conn) error {
	if h.Ready == nil {
		return server.ErrAccessDenied
	}
	return h.Ready(c)
}
func (h *IdentityHandler) OnAuthFailure(c *server.Conn, e error) {
	if h.Failure != nil {
		h.Failure(c, e)
	}
}

func NewServer(config *tls.Config, verify func(context.Context, string, string) error) *server.Server {
	safe := config.Clone()
	safe.MinVersion = tls.VersionTLS12
	return server.NewServerWithAuth("8.4.0-NextTerminal", mysql.DEFAULT_COLLATION_ID, mysql.AUTH_CACHING_SHA2_PASSWORD, nil, safe, &FullAuth{Verify: verify})
}
