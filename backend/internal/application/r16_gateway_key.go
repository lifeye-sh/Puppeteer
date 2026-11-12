package application

import (
	"context"
	"crypto/rsa"
	"encoding/pem"
	"golang.org/x/crypto/ssh"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/secretbox"
	"strings"
)

func parseConfiguredGatewayKey(raw []byte) (ssh.Signer, error) {
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "-----BEGIN ") {
		return nil, fault.Invalid
	}
	block, rest := pem.Decode([]byte(trimmed))
	if block == nil || len(block.Headers) != 0 || strings.TrimSpace(string(rest)) != "" {
		return nil, fault.Invalid
	}
	signer, e := ssh.ParsePrivateKey(raw)
	if e != nil {
		return nil, fault.Invalid
	}
	switch signer.PublicKey().Type() {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
	case ssh.KeyAlgoRSA:
		key, ok := signer.PublicKey().(ssh.CryptoPublicKey)
		if !ok {
			return nil, fault.Invalid
		}
		rsaKey, ok := key.CryptoPublicKey().(*rsa.PublicKey)
		if !ok || rsaKey.N.BitLen() < 2048 {
			return nil, fault.Invalid
		}
	default:
		return nil, fault.Invalid
	}
	return signer, nil
}

func (a *App) configuredGatewayKey(ctx context.Context, v system.ConfigurationValues) (ssh.Signer, error) {
	ref := v.String("ssh-server-private-key")
	if ref == "" {
		return a.nativeGatewayKey(ctx)
	}
	var sealed []byte
	if e := a.db.Row(ctx, `SELECT sealed FROM configuration_secrets WHERE id=$1 AND key='ssh-server-private-key'`, ref).Scan(&sealed); e != nil {
		return nil, e
	}
	plain, e := secretbox.Open(a.config.VaultKey, sealed, "settings:ssh-server-private-key:"+ref)
	if e != nil {
		return nil, e
	}
	defer clear(plain)
	return parseConfiguredGatewayKey(plain)
}

// The active listener generation, not the requested settings revision, owns the
// public key advertised to clients. Failed prepare keeps the old fingerprint.
func (a *App) effectiveGatewayPublicKey(ctx context.Context) (string, error) {
	a.configurationMu.Lock()
	if a.listenerManager != nil {
		defer a.configurationMu.Unlock()
		for _, v := range a.listenerManager.States() {
			if v.Name == "ssh" {
				return v.Identity, nil
			}
		}
		return "", nil
	}
	a.configurationMu.Unlock()
	// Compatibility for isolated protocol fixtures running an explicit listener.
	var public string
	e := a.db.Row(ctx, `SELECT public_key FROM native_ssh_gateway_keys WHERE singleton=1`).Scan(&public)
	return strings.TrimSpace(public), e
}
