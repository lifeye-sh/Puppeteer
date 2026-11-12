package vault

import (
	"context"
	"next-terminal/backend/internal/modules/databaseauth"
	"next-terminal/backend/internal/platform/secretbox"
)

// StoreDatabaseVerifier never accepts or persists the plaintext database password.
// Caller owns the identity lock and transaction for rotation/revocation.
func (s *Service) StoreDatabaseVerifier(ctx context.Context, userID, verifier string, days int) error {
	_, scramError := databaseauth.SCRAMCredentials(verifier)
	cipher, key, e := secretbox.Wrap(s.key, []byte(verifier), "database-password:"+userID)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO database_passwords(user_id,ciphertext,wrapped_key,expires_at,scram_ready) VALUES($1,$2,$3,now()+($4*interval '1 day'),$5) ON CONFLICT(user_id) DO UPDATE SET ciphertext=excluded.ciphertext,wrapped_key=excluded.wrapped_key,scram_ready=excluded.scram_ready,version=database_passwords.version+1,expires_at=excluded.expires_at,revoked_at=NULL,updated_at=now()`, userID, cipher, key, days, scramError == nil)
	return e
}

func (s *Service) DatabaseVerifier(ctx context.Context, user string, version int64) ([]byte, error) {
	var ciphertext, key []byte
	if e := s.db.Row(ctx, `SELECT ciphertext,wrapped_key FROM database_passwords WHERE user_id=$1 AND version=$2 AND revoked_at IS NULL AND expires_at>now()`, user, version).Scan(&ciphertext, &key); e != nil {
		return nil, e
	}
	return secretbox.Unwrap(s.key, ciphertext, key, "database-password:"+user)
}

// DatabaseAuth is called only after the application checks the current grant.
// The caller owns the transaction to pin credential metadata to its secret.
func (s *Service) DatabaseAuth(ctx context.Context, id, actor, operation string) (Credential, Secret, error) {
	var locked string
	if e := s.db.Row(ctx, `SELECT id FROM credentials WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, id).Scan(&locked); e != nil {
		return Credential{}, Secret{}, e
	}
	credential, e := s.Get(ctx, id)
	if e != nil {
		return credential, Secret{}, e
	}
	secret, e := s.readSecret(ctx, id, actor, operation, "database_connection")
	return credential, secret, e
}
