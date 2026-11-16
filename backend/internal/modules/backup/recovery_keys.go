package backup

import (
	"context"
	"errors"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"puppeteer/backend/internal/platform/fault"
	"puppeteer/backend/internal/platform/secretbox"
)

// Use the existing encrypted configuration store and its restore-verified AAD.
// These entries are append-only: old archives still require their original key.
const recoveryKeyPrefix = "backup-recovery:"

type RecoveryKey struct {
	ID        string    `json:"id"`
	Recipient string    `json:"recipient"`
	CreatedAt time.Time `json:"createdAt"`
	Active    bool      `json:"active"`
}

func (s *Service) EnsureRecoveryKey(ctx context.Context, vaultKey []byte) error {
	return s.db.Within(ctx, func(c context.Context) error {
		_, err := s.generateRecoveryKey(c, vaultKey, nil, "")
		return err
	})
}

// RotateRecoveryKey must participate in the caller's authorization/audit transaction.
func (s *Service) RotateRecoveryKey(ctx context.Context, vaultKey []byte, revision int64, actor string) (RecoveryKey, error) {
	if !s.db.InTransaction(ctx) {
		return RecoveryKey{}, errors.New("recovery rotation requires a transaction")
	}
	return s.generateRecoveryKey(ctx, vaultKey, &revision, actor)
}

func (s *Service) generateRecoveryKey(ctx context.Context, vaultKey []byte, expected *int64, actor string) (out RecoveryKey, err error) {
	var recipient string
	var revision int64
	if err = s.db.Row(ctx, `SELECT recipient,revision FROM backup_config WHERE singleton=true FOR UPDATE`).Scan(&recipient, &revision); err != nil {
		return
	}
	if expected == nil && recipient != "" {
		return
	}
	if expected != nil && (*expected < 1 || *expected != revision) {
		return out, fault.Conflict
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		return out, err
	}
	out.ID, out.Recipient, out.Active = uuid.NewString(), key.Recipient().String(), true
	name := recoveryKeyPrefix + out.Recipient
	plain := []byte(key.String())
	defer clear(plain)
	sealed, err := secretbox.Seal(vaultKey, plain, "settings:"+name+":"+out.ID)
	if err != nil {
		return out, err
	}
	if err = s.db.Row(ctx, `INSERT INTO configuration_secrets(id,key,sealed,created_by) VALUES($1,$2,$3,NULLIF($4,'')::uuid) RETURNING created_at`, out.ID, name, sealed, actor).Scan(&out.CreatedAt); err != nil {
		return
	}
	_, err = s.db.Exec(ctx, `UPDATE backup_config SET recipient=$1,revision=revision+1 WHERE singleton=true`, out.Recipient)
	return
}

func (s *Service) RecoveryKeys(ctx context.Context) ([]RecoveryKey, error) {
	rows, err := s.db.Rows(ctx, `SELECT s.id::text,substring(s.key from $1::int),s.created_at,(s.key=$2||b.recipient) FROM configuration_secrets s CROSS JOIN backup_config b WHERE b.singleton=true AND starts_with(s.key,$2) ORDER BY s.created_at DESC,s.id`, len(recoveryKeyPrefix)+1, recoveryKeyPrefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecoveryKey{}
	for rows.Next() {
		var v RecoveryKey
		if err = rows.Scan(&v.ID, &v.Recipient, &v.CreatedAt, &v.Active); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Service) RecoveryIdentity(ctx context.Context, vaultKey []byte, id string) ([]byte, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, fault.Invalid
	}
	var name string
	var sealed []byte
	if err := s.db.Row(ctx, `SELECT key,sealed FROM configuration_secrets WHERE id=$1 AND starts_with(key,$2)`, id, recoveryKeyPrefix).Scan(&name, &sealed); err != nil {
		return nil, err
	}
	plain, err := secretbox.Open(vaultKey, sealed, "settings:"+name+":"+id)
	if err != nil {
		return nil, err
	}
	key, err := age.ParseX25519Identity(string(plain))
	if err != nil || key.Recipient().String() != strings.TrimPrefix(name, recoveryKeyPrefix) {
		clear(plain)
		return nil, errors.New("invalid managed recovery key")
	}
	return plain, nil
}
