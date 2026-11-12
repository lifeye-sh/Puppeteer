package application

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"github.com/google/uuid"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/oauthkeys"
	"next-terminal/backend/internal/platform/secretbox"
	"strings"
	"unicode"
)

// Check the public input before entering the password-proof transaction. Empty
// and masked values mean keep; clearing is always an explicit separate action.
func settingsSecretInput(patch map[string]json.RawMessage, clearKeys []string) (bool, error) {
	reauth := false
	seen := map[string]bool{}
	for _, key := range clearKeys {
		if !system.IsConfigurationSecret(key) || seen[key] {
			return false, fault.Invalid
		}
		seen[key] = true
		reauth = true
	}
	for key, raw := range patch {
		if !system.IsConfigurationSecret(key) {
			continue
		}
		var value string
		maxSize := 8192
		if key == "ldap-password" {
			maxSize = 65536
		}
		if string(raw) == "null" || json.Unmarshal(raw, &value) != nil || len(value) > maxSize || (key == "ldap-password" && strings.ContainsRune(value, 0)) {
			return false, fault.Invalid
		}
		if value != "" && value != system.SecretMask {
			if key == "llm-api-key" && (len(value) > 4096 || strings.IndexFunc(value, unicode.IsControl) >= 0) {
				return false, fault.Invalid
			}
			if key == system.OAuthSigningKey {
				if _, e := oauthkeys.Signing([]byte(value)); e != nil {
					return false, fault.Invalid
				}
			}
			if key == system.OAuthHMACKey {
				raw, e := oauthkeys.HMAC(value)
				clear(raw)
				if e != nil {
					return false, fault.Invalid
				}
			}
			if key == "ssh-server-private-key" {
				if _, err := parseConfiguredGatewayKey([]byte(value)); err != nil {
					return false, err
				}
			}
			if seen[key] {
				return false, fault.Invalid
			}
			reauth = true
		}
	}
	return reauth, nil
}

func (a *App) prepareConfigurationSecrets(ctx context.Context, patch map[string]json.RawMessage, clearKeys []string, actor string) error {
	current, err := a.system.Configuration(ctx, 0)
	if err != nil {
		return err
	}
	for key, raw := range patch {
		if !system.IsConfigurationSecret(key) {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return fault.Invalid
		}
		reference := current.Values.String(key)
		if value != "" && value != system.SecretMask {
			if key == system.OAuthHMACKey {
				raw, e := oauthkeys.HMAC(value)
				if e != nil {
					return fault.Invalid
				}
				reused := subtle.ConstantTimeCompare(raw, a.config.VaultKey) == 1 || subtle.ConstantTimeCompare(raw, a.config.EncryptionKey) == 1
				clear(raw)
				if reused {
					return fault.Invalid
				}
			}
			reference = uuid.NewString()
			sealed, e := secretbox.Seal(a.config.VaultKey, []byte(value), "settings:"+key+":"+reference)
			if e != nil {
				return e
			}
			if _, e = a.db.Exec(ctx, `INSERT INTO configuration_secrets(id,key,sealed,created_by) VALUES($1,$2,$3,$4)`, reference, key, sealed, actor); e != nil {
				return e
			}
		}
		patch[key], err = json.Marshal(reference)
		if err != nil {
			return err
		}
	}
	for _, key := range clearKeys {
		patch[key] = json.RawMessage(`""`)
	}
	return nil
}
