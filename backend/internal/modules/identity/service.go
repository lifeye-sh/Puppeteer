package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"time"
)

type User struct {
	MFAVerifiedAt          *time.Time `json:"-"`
	ContactChannelVersion  int64      `json:"-"`
	PasskeyID              string     `json:"-"`
	ContactID              string     `json:"-"`
	ContactVersion         int64      `json:"-"`
	ContactSettingsVersion int64      `json:"-"`
	LDAPIdentityID         string     `json:"-"`
	LDAPProviderVersion    int64      `json:"-"`
	ExternalIdentityID     string     `json:"-"`
	OIDCProviderVersion    int64      `json:"-"`
	ID                     string     `json:"id"`
	Username               string     `json:"username"`
	Nickname               string     `json:"nickname"`
	Type                   string     `json:"type"`
	Phase                  string     `json:"phase,omitempty"`
}
type Service struct {
	db    *postgres.DB
	dummy string
	key   []byte
}

func New(db *postgres.DB, key []byte) *Service {
	return &Service{db: db, dummy: HashPassword(NewToken()), key: key}
}
func (s *Service) CreateAdmin(ctx context.Context, username, nickname, hash string) (u User, e error) {
	if !s.db.InTransaction(ctx) {
		e = s.db.Within(ctx, func(c context.Context) error {
			var err error
			u, err = s.CreateAdmin(c, username, nickname, hash)
			return err
		})
		return
	}
	var initialized bool
	if e = s.db.Row(ctx, `SELECT initialized_at IS NOT NULL FROM instance_state WHERE singleton=1`).Scan(&initialized); e != nil {
		return
	}
	// Initial setup owns the singleton row lock; its one recovery administrator
	// must be creatable before a signed license can be imported.
	if initialized {
		if e = s.db.Admit(ctx, "users", "identity.users"); e != nil {
			return
		}
	}
	e = s.db.Row(ctx, `INSERT INTO users(username,nickname,password_hash,type) VALUES($1,$2,$3,'admin') RETURNING id,username,nickname,type`, username, nickname, hash).Scan(&u.ID, &u.Username, &u.Nickname, &u.Type)
	return
}
func (s *Service) Verify(ctx context.Context, username, password string) (*User, error) {
	var u User
	var hash, status string
	e := s.db.Row(ctx, `SELECT id,username,nickname,type,password_hash,status FROM users WHERE username=$1 AND deleted_at IS NULL FOR UPDATE`, username).Scan(&u.ID, &u.Username, &u.Nickname, &u.Type, &hash, &status)
	if errors.Is(e, pgx.ErrNoRows) {
		CheckPassword(s.dummy, password)
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if !CheckPassword(hash, password) || status != "enabled" {
		return nil, nil
	}
	return &u, nil
}
func Digest(token string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(token))) }

var ErrSessionLimit = errors.New("browser session capacity reached")

func (s *Service) Issue(ctx context.Context, u User, epoch int64, ip string) (string, error) {
	if !s.db.InTransaction(ctx) {
		var token string
		err := s.db.Within(ctx, func(c context.Context) error { var e error; token, e = s.Issue(c, u, epoch, ip); return e })
		return token, err
	}
	// All identity methods share the same account row lock before counting and
	// issuing. Verify already owns this row; do not introduce a reversed lock order.
	var enabled bool
	if err := s.db.Row(ctx, `SELECT status='enabled' AND deleted_at IS NULL FROM users WHERE id=$1 FOR UPDATE`, u.ID).Scan(&enabled); err != nil {
		return "", err
	}
	if !enabled {
		return "", fault.Denied
	}
	token := NewToken()
	if u.Phase == "" {
		u.Phase = "authenticated"
	}
	configuration, err := system.New(s.db).SecurityConfiguration(ctx)
	if err != nil {
		return "", err
	}
	if err = system.ValidateConfiguration(configuration.Values); err != nil {
		return "", err
	}
	if u.Phase == "authenticated" && configuration.Values.Bool("login-force-totp-enabled") && u.MFAVerifiedAt == nil {
		enabled, _, e := s.MFAStatus(ctx, u.ID)
		if e != nil {
			return "", e
		}
		u.Phase = "mfa_enroll"
		if enabled {
			u.Phase = "mfa_pending"
		}
	}
	if u.Phase == "authenticated" && u.UsesLocalPassword() {
		expired, e := s.PasswordExpired(ctx, u.ID)
		if e != nil {
			return "", e
		}
		if expired {
			u.Phase = "password_change"
		}
	}
	if u.Phase == "authenticated" && configuration.Values.Bool("login-session-count-custom") {
		var active int
		if err = s.db.Row(ctx, `SELECT count(*) FROM auth_sessions a JOIN users u ON u.id=a.user_id CROSS JOIN instance_state i WHERE a.user_id=$1 AND a.phase='authenticated' AND a.auth_epoch=$2 AND `+validAuthSessionSQL, u.ID, epoch).Scan(&active); err != nil {
			return "", err
		}
		if active >= configuration.Values.Int("login-session-count-limit") {
			return "", ErrSessionLimit
		}
	}
	minutes := configuration.Values.Int("login-session-duration")
	if minutes < 1 || minutes > 525600 {
		return "", fault.Invalid
	}
	ttl := int64(minutes) * 60
	if u.Phase != "authenticated" {
		ttl = 300
	}
	_, e := s.db.Exec(ctx, `INSERT INTO auth_sessions(token_hash,user_id,auth_epoch,absolute_expires_at,source_ip,phase,external_identity_id,oidc_provider_version,ldap_identity_id,ldap_provider_version,contact_id,contact_version,contact_settings_version,passkey_id,contact_channel_version,configuration_revision,browser_session,mfa_verified_at) VALUES($1,$2,$3,now()+$4*interval '1 second',$5,$6,nullif($7,'')::uuid,$8,nullif($9,'')::uuid,$10,nullif($11,'')::uuid,$12,$13,nullif($14,'')::uuid,$15,$16,$17,$18)`, Digest(token), u.ID, epoch, ttl, ip, u.Phase, u.ExternalIdentityID, u.OIDCProviderVersion, u.LDAPIdentityID, u.LDAPProviderVersion, u.ContactID, u.ContactVersion, u.ContactSettingsVersion, u.PasskeyID, u.ContactChannelVersion, configuration.ID, configuration.Values.Bool("login-session-browser-close-logout"), u.MFAVerifiedAt)
	if e == nil {
		e = s.RecordLoginProof(ctx, &u, epoch)
	}
	return token, e
}
func (s *Service) Current(ctx context.Context, token string) (*User, error) {
	u, e := s.Session(ctx, token)
	if u != nil && u.Phase != "authenticated" {
		return nil, nil
	}
	return u, e
}
func (s *Service) Session(ctx context.Context, token string) (*User, error) {
	if len(token) != 64 {
		return nil, nil
	}
	var u User
	e := s.db.Row(ctx, `SELECT u.id,u.username,u.nickname,u.type,a.phase,coalesce(a.external_identity_id::text,''),coalesce(a.oidc_provider_version,0),coalesce(a.ldap_identity_id::text,''),coalesce(a.ldap_provider_version,0),coalesce(a.contact_id::text,''),coalesce(a.contact_version,0),coalesce(a.contact_settings_version,0),coalesce(a.passkey_id::text,''),coalesce(a.contact_channel_version,0),a.mfa_verified_at FROM auth_sessions a JOIN users u ON u.id=a.user_id CROSS JOIN instance_state i WHERE a.token_hash=$1 AND `+validAuthSessionSQL, Digest(token)).Scan(&u.ID, &u.Username, &u.Nickname, &u.Type, &u.Phase, &u.ExternalIdentityID, &u.OIDCProviderVersion, &u.LDAPIdentityID, &u.LDAPProviderVersion, &u.ContactID, &u.ContactVersion, &u.ContactSettingsVersion, &u.PasskeyID, &u.ContactChannelVersion, &u.MFAVerifiedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if u.Phase == "authenticated" && u.MFAVerifiedAt == nil {
		security, err := system.New(s.db).SecurityConfiguration(ctx)
		if err != nil {
			return nil, err
		}
		if security.Values.Bool("login-force-totp-enabled") {
			enabled, _, err := s.MFAStatus(ctx, u.ID)
			if err != nil {
				return nil, err
			}
			u.Phase = "mfa_enroll"
			if enabled {
				u.Phase = "mfa_pending"
			}
			if _, err = s.db.Exec(ctx, `UPDATE auth_sessions SET phase=$2,absolute_expires_at=LEAST(absolute_expires_at,now()+interval '5 minutes') WHERE token_hash=$1 AND phase='authenticated'`, Digest(token), u.Phase); err != nil {
				return nil, err
			}
		}
	}
	if u.Phase == "authenticated" && u.UsesLocalPassword() {
		expired, err := s.PasswordExpired(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		if expired {
			if _, err = s.db.Exec(ctx, `UPDATE auth_sessions SET phase='password_change',absolute_expires_at=LEAST(absolute_expires_at,now()+interval '5 minutes') WHERE token_hash=$1 AND phase='authenticated'`, Digest(token)); err != nil {
				return nil, err
			}
			u.Phase = "password_change"
		}
	}
	return &u, nil
}
func (s *Service) Revoke(ctx context.Context, token string) error {
	_, e := s.db.Exec(ctx, `UPDATE auth_sessions SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, Digest(token))
	return e
}

// CookieLifetime uses the persisted expiry, not a later configuration revision.
// Zero MaxAge is allowed only for a persisted browser-session cookie preference; expired rows still return an error.
func (s *Service) CookieLifetime(ctx context.Context, token string) (seconds int, err error) {
	err = s.db.Row(ctx, `SELECT CASE WHEN browser_session THEN 0 ELSE greatest(1,floor(extract(epoch FROM absolute_expires_at-clock_timestamp())))::integer END FROM auth_sessions WHERE token_hash=$1 AND revoked_at IS NULL AND absolute_expires_at>clock_timestamp()`, Digest(token)).Scan(&seconds)
	return
}
