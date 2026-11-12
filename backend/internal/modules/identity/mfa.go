package identity

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"time"
)

func (s *Service) cryptSeal(user string, plain []byte) ([]byte, error) {
	b, e := aes.NewCipher(s.key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, g.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return g.Seal(nonce, nonce, plain, []byte(user)), nil
}
func (s *Service) cryptOpen(user string, sealed []byte) ([]byte, error) {
	b, e := aes.NewCipher(s.key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	if len(sealed) < g.NonceSize() {
		return nil, fault.Proof
	}
	return g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], []byte(user))
}

// TOTP follows RFC 6238 / RFC 4226: HMAC-SHA1, six digits, 30-second steps.
func TOTP(secret []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	h := hmac.New(sha1.New, secret)
	h.Write(counter[:])
	sum := h.Sum(nil)
	off := sum[len(sum)-1] & 15
	n := (binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", n)
}
func validStep(secret []byte, code string, last int64) int64 {
	if len(code) != 6 {
		return -1
	}
	now := time.Now().Unix() / 30
	for _, step := range []int64{now, now - 1, now + 1} {
		if step > last && subtle.ConstantTimeCompare([]byte(TOTP(secret, step)), []byte(code)) == 1 {
			return step
		}
	}
	return -1
}
func (s *Service) MFAStatus(ctx context.Context, id string) (enabled, required bool, e error) {
	configuration, err := system.New(s.db).SecurityConfiguration(ctx)
	if err != nil {
		return false, false, err
	}
	e = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM mfa_factors WHERE user_id=$1 AND secret_cipher IS NOT NULL),force_mfa OR $2 FROM users WHERE id=$1`, id, configuration.Values.Bool("login-force-totp-enabled")).Scan(&enabled, &required)
	return
}
func (s *Service) LockUser(ctx context.Context, id string) error {
	var v string
	return s.db.Row(ctx, `SELECT id FROM users WHERE id=$1 AND status='enabled' AND deleted_at IS NULL FOR UPDATE`, id).Scan(&v)
}
func (s *Service) BeginMFA(ctx context.Context, u User) (map[string]string, error) {
	enabled, _, e := s.MFAStatus(ctx, u.ID)
	if e != nil {
		return nil, e
	}
	if enabled && u.Phase != "mfa_enroll" {
		return nil, fault.Conflict
	}
	secret := make([]byte, 20)
	if _, e = rand.Read(secret); e != nil {
		return nil, e
	}
	sealed, e := s.cryptSeal(u.ID, secret)
	if e != nil {
		return nil, e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO mfa_factors(user_id,pending_cipher,pending_expires_at) VALUES($1,$2,now()+interval '5 minutes') ON CONFLICT(user_id) DO UPDATE SET pending_cipher=$2,pending_expires_at=now()+interval '5 minutes'`, u.ID, sealed)
	if e != nil {
		return nil, e
	}
	key := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	uri := "otpauth://totp/" + url.PathEscape("Puppeteer:"+u.Username) + "?secret=" + key + "&issuer=Puppeteer&algorithm=SHA1&digits=6&period=30"
	return map[string]string{"secret": key, "uri": uri}, nil
}
func (s *Service) ConfirmMFA(ctx context.Context, id, code string) ([]string, error) {
	var sealed []byte
	var expiry *time.Time
	if e := s.db.Row(ctx, `SELECT pending_cipher,pending_expires_at FROM mfa_factors WHERE user_id=$1 FOR UPDATE`, id).Scan(&sealed, &expiry); e != nil {
		return nil, e
	}
	if expiry == nil || expiry.Before(time.Now()) || len(sealed) == 0 {
		return nil, fault.Proof
	}
	plain, e := s.cryptOpen(id, sealed)
	if e != nil {
		return nil, e
	}
	step := validStep(plain, code, -1)
	if step < 0 {
		return nil, fault.Proof
	}
	if _, e = s.db.Exec(ctx, `UPDATE mfa_factors SET secret_cipher=pending_cipher,pending_cipher=NULL,pending_expires_at=NULL,last_step=$2 WHERE user_id=$1`, id, step); e != nil {
		return nil, e
	}
	if _, e = s.db.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id=$1`, id); e != nil {
		return nil, e
	}
	codes := []string{}
	for i := 0; i < 10; i++ {
		code := NewToken()[:32]
		codes = append(codes, code)
		if _, e = s.db.Exec(ctx, `INSERT INTO mfa_recovery_codes(user_id,code_hash) VALUES($1,$2)`, id, Digest(id+":"+code)); e != nil {
			return nil, e
		}
	}
	_, e = s.db.Exec(ctx, `UPDATE users SET force_mfa=false WHERE id=$1`, id)
	return codes, e
}
func (s *Service) ConsumeTOTP(ctx context.Context, id, code string) error {
	var sealed []byte
	var last int64
	e := s.db.Row(ctx, `SELECT secret_cipher,last_step FROM mfa_factors WHERE user_id=$1 FOR UPDATE`, id).Scan(&sealed, &last)
	if e != nil {
		return e
	}
	if len(sealed) == 0 {
		return fault.Proof
	}
	plain, e := s.cryptOpen(id, sealed)
	if e != nil {
		return e
	}
	step := validStep(plain, code, last)
	if step < 0 {
		return fault.Proof
	}
	_, e = s.db.Exec(ctx, `UPDATE mfa_factors SET last_step=$2 WHERE user_id=$1`, id, step)
	return e
}
func (s *Service) ConsumeRecovery(ctx context.Context, id, code string) error {
	code = strings.TrimSpace(code)
	if len(code) != 32 {
		return fault.Proof
	}
	tag, e := s.db.Exec(ctx, `UPDATE mfa_recovery_codes SET used_at=now() WHERE user_id=$1 AND code_hash=$2 AND used_at IS NULL`, id, Digest(id+":"+code))
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Proof
	}
	return nil
}
func (s *Service) MFAFailure(ctx context.Context, token string) error {
	_, e := s.db.Exec(ctx, `UPDATE auth_sessions SET mfa_attempts=mfa_attempts+1,revoked_at=CASE WHEN mfa_attempts>=4 THEN now() ELSE revoked_at END WHERE token_hash=$1`, Digest(token))
	return e
}
func (s *Service) ResetMFA(ctx context.Context, id string) error {
	if e := s.LockUser(ctx, id); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `DELETE FROM mfa_factors WHERE user_id=$1`, id); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id=$1`, id); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `UPDATE users SET force_mfa=true WHERE id=$1`, id); e != nil {
		return e
	}
	return s.RevokeAll(ctx, id)
}
