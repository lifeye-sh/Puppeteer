package backup

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/platform/secretbox"
)

// RecoveryBundle is serialized only inside age ciphertext. The deployment's
// two current local keys are explicit; unknown future key IDs fail closed.
type RecoveryBundle struct {
	Version  int    `json:"version"`
	MFAKey   string `json:"mfaKey"`
	VaultKey string `json:"vaultKey"`
}

func ParseRecoveryBundle(raw []byte) (RecoveryBundle, error) {
	var k RecoveryBundle
	if len(raw) > 16384 || json.Unmarshal(raw, &k) != nil || k.Version != 1 {
		return k, errors.New("invalid recovery bundle")
	}
	for _, v := range []string{k.MFAKey, k.VaultKey} {
		b, e := hex.DecodeString(v)
		if e != nil || len(b) != 32 {
			return k, errors.New("invalid recovery key")
		}
		clear(b)
	}
	return k, nil
}
func (k RecoveryBundle) Verify(ctx context.Context, c *pgx.Conn) error {
	mfa, _ := hex.DecodeString(k.MFAKey)
	vault, _ := hex.DecodeString(k.VaultKey)
	defer clear(mfa)
	defer clear(vault)
	if k.Version != 1 || len(mfa) != 32 || len(vault) != 32 {
		return errors.New("invalid recovery key")
	}
	var mf, vf string
	if e := c.QueryRow(ctx, "SELECT mfa_key_fingerprint,vault_key_fingerprint FROM instance_state WHERE singleton=1").Scan(&mf, &vf); e != nil {
		return errors.New("key fingerprints unavailable")
	}
	mh, vh := sha256.Sum256(mfa), sha256.Sum256(vault)
	if mf != hex.EncodeToString(mh[:]) || vf != hex.EncodeToString(vh[:]) {
		return errors.New("recovery keys do not match snapshot")
	}
	rows, e := c.Query(ctx, "SELECT credential_id::text,version,ciphertext,wrapped_dek,kek_id FROM secret_versions")
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, kek string
		var version int64
		var sealed, wrapped []byte
		if e = rows.Scan(&id, &version, &sealed, &wrapped, &kek); e != nil {
			rows.Close()
			return e
		}
		if kek != "local-v1" {
			rows.Close()
			return errors.New("unsupported credential key version")
		}
		plain, e := secretbox.Unwrap(vault, sealed, wrapped, fmt.Sprintf("credential:%s:%d", id, version))
		clear(plain)
		if e != nil {
			rows.Close()
			return errors.New("credential decryption verification failed")
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = c.Query(ctx, "SELECT id::text,pem,key_sealed FROM certificates")
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var id, pem string
		var sealed []byte
		if e = rows.Scan(&id, &pem, &sealed); e != nil {
			return e
		}
		key, e := secretbox.Open(vault, sealed, "certificate:"+id)
		if e != nil {
			return errors.New("certificate key decryption failed")
		}
		if len(key) == 0 {
			continue
		}
		_, e = tls.X509KeyPair([]byte(pem), key)
		clear(key)
		if e != nil {
			return errors.New("certificate public and private keys do not match")
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	// Validate every encrypted value, including disabled integrations. No remote
	// authentication or delivery occurs during restore verification.
	for _, item := range []struct {
		query string
		key   []byte
	}{
		{`SELECT user_id::text,secret_cipher FROM mfa_factors WHERE secret_cipher IS NOT NULL`, mfa},
		{`SELECT 'notification:'||id::text,secret FROM notification_channels WHERE octet_length(secret)>0`, vault},
		{`SELECT 'acme:'||id::text,key_sealed FROM acme_accounts`, vault},
		{`SELECT 'dns:'||id::text,secret_sealed FROM dns_providers`, vault},
		{`SELECT 'backup-destination:'||id::text,secret FROM backup_destinations`, vault},
		{`SELECT 'backup-destination:'||destination_id::text,secret FROM backup_copies`, vault},
	} {
		r, e := c.Query(ctx, item.query)
		if e != nil {
			return e
		}
		for r.Next() {
			var aad string
			var sealed []byte
			if e = r.Scan(&aad, &sealed); e != nil {
				r.Close()
				return e
			}
			plain, e := secretbox.Open(item.key, sealed, aad)
			clear(plain)
			if e != nil {
				r.Close()
				return errors.New("encrypted integration or MFA verification failed")
			}
		}
		e = r.Err()
		r.Close()
		if e != nil {
			return e
		}
	}
	var hasAI bool
	var hasSettingsSecrets bool
	if e := c.QueryRow(ctx, `SELECT to_regclass('configuration_secrets') IS NOT NULL`).Scan(&hasSettingsSecrets); e != nil {
		return e
	}
	if hasSettingsSecrets {
		rows, e := c.Query(ctx, `SELECT 'settings:'||key||':'||id::text,sealed FROM configuration_secrets`)
		if e != nil {
			return e
		}
		for rows.Next() {
			var aad string
			var sealed []byte
			if e = rows.Scan(&aad, &sealed); e != nil {
				rows.Close()
				return e
			}
			plain, err := secretbox.Open(vault, sealed, aad)
			clear(plain)
			if err != nil {
				rows.Close()
				return errors.New("encrypted configuration verification failed")
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	if e := c.QueryRow(ctx, `SELECT to_regclass('ai_profiles') IS NOT NULL`).Scan(&hasAI); e != nil {
		return e
	}
	if hasAI {
		r, e := c.Query(ctx, `SELECT id::text,secret FROM ai_profiles WHERE octet_length(secret)>0`)
		if e != nil {
			return e
		}
		defer r.Close()
		for r.Next() {
			var id string
			var sealed []byte
			if e = r.Scan(&id, &sealed); e != nil {
				return e
			}
			plain, e := secretbox.Open(vault, sealed, "ai-profile:"+id)
			clear(plain)
			if e != nil {
				return errors.New("AI model key verification failed")
			}
		}
		if e = r.Err(); e != nil {
			return e
		}
	}
	var hasJobs bool
	if e := c.QueryRow(ctx, `SELECT to_regclass('ai_jobs') IS NOT NULL`).Scan(&hasJobs); e != nil {
		return e
	}
	if hasJobs {
		rows, e := c.Query(ctx, `SELECT turn_id::text,payload FROM ai_jobs WHERE octet_length(payload)>0`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var sealed []byte
			if e = rows.Scan(&id, &sealed); e != nil {
				return e
			}
			plain, err := secretbox.Open(vault, sealed, "ai-job:"+id)
			clear(plain)
			if err != nil {
				return errors.New("AI queue key verification failed")
			}
		}
		if e = rows.Err(); e != nil {
			return e
		}
	}
	return nil
}
