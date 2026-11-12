package application

import (
	"context"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/oauthkeys"
	"next-terminal/backend/internal/platform/secretbox"
	"slices"
)

func (a *App) applyOAuthKeyConfiguration(ctx context.Context, previous, next system.ConfigurationValues) error {
	for _, key := range []string{system.OAuthSigningKey, system.OAuthHMACKey} {
		reference := next.String(key)
		if previous.String(key) == reference && !slices.Contains(system.OAuthKeyRotations(ctx), key) {
			continue
		}
		if reference == "" {
			if key == system.OAuthSigningKey {
				if e := a.identity.EnsureOAuthKey(ctx, true); e != nil {
					return e
				}
			} else {
				if e := a.identity.EnsureOAuthHMACKey(ctx, true); e != nil {
					return e
				}
			}
			continue
		}
		var sealed []byte
		if e := a.db.Row(ctx, `SELECT sealed FROM configuration_secrets WHERE id=$1 AND key=$2`, reference, key).Scan(&sealed); e != nil {
			return e
		}
		raw, e := secretbox.Open(a.config.VaultKey, sealed, "settings:"+key+":"+reference)
		if e != nil {
			return e
		}
		if key == system.OAuthSigningKey {
			e = a.identity.ImportOAuthSigningKey(ctx, raw)
		} else {
			var decoded []byte
			decoded, e = oauthkeys.HMAC(string(raw))
			if e == nil {
				e = a.identity.ImportOAuthHMACKey(ctx, decoded)
			}
			clear(decoded)
		}
		clear(raw)
		if e != nil {
			return e
		}
	}
	return nil
}
