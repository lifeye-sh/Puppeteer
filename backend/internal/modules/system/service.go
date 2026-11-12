package system

import (
	"context"
	"next-terminal/backend/internal/platform/postgres"
)

type State struct {
	InstanceID    string `json:"instanceId"`
	Initialized   bool   `json:"initialized"`
	SchemaVersion int    `json:"schemaVersion"`
	AuthEpoch     int64  `json:"-"`
}
type Service struct{ db *postgres.DB }

func New(db *postgres.DB) *Service { return &Service{db} }
func (s *Service) CheckVaultKey(ctx context.Context, fingerprint string) error {
	var value string
	return s.db.Row(ctx, `UPDATE instance_state SET vault_key_fingerprint=$1 WHERE vault_key_fingerprint='' OR vault_key_fingerprint=$1 RETURNING vault_key_fingerprint`, fingerprint).Scan(&value)
}
func (s *Service) CheckMFAKey(ctx context.Context, fingerprint string) error {
	var value string
	return s.db.Row(ctx, `UPDATE instance_state SET mfa_key_fingerprint=$1 WHERE mfa_key_fingerprint='' OR mfa_key_fingerprint=$1 RETURNING mfa_key_fingerprint`, fingerprint).Scan(&value)
}
func (s *Service) State(ctx context.Context) (v State, e error) {
	e = s.db.Row(ctx, `SELECT instance_id,initialized_at IS NOT NULL,schema_version,auth_epoch FROM instance_state WHERE singleton=1`).Scan(&v.InstanceID, &v.Initialized, &v.SchemaVersion, &v.AuthEpoch)
	return
}
func (s *Service) LockSetup(ctx context.Context) (done bool, e error) {
	e = s.db.Row(ctx, `SELECT initialized_at IS NOT NULL FROM instance_state WHERE singleton=1 FOR UPDATE`).Scan(&done)
	return
}
func (s *Service) Initialize(ctx context.Context) error {
	_, e := s.db.Exec(ctx, `UPDATE instance_state SET initialized_at=now() WHERE singleton=1`)
	return e
}
func (s *Service) Settings(ctx context.Context) (map[string]any, error) {
	rows, e := s.db.Rows(ctx, `SELECT key,typed_value FROM settings ORDER BY key`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := map[string]any{}
	for rows.Next() {
		var key string
		var value any
		if e = rows.Scan(&key, &value); e != nil {
			return nil, e
		}
		if IsConfigurationSecret(key) {
			value = MaskConfigurationSecret(value)
		}
		result[key] = value
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	rows.Close()
	readonly, e := s.ReadonlyConfiguration(ctx)
	if e != nil {
		return nil, e
	}
	for k, v := range readonly {
		result[k] = v
	}
	return result, nil
}
