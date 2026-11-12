package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/mail"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/secretbox"
	"regexp"
	"strings"
	"time"
)

type ContactSettings struct {
	EmailChannel    string `json:"emailChannel"`
	SMSChannel      string `json:"smsChannel"`
	RecoveryEnabled bool   `json:"recoveryEnabled"`
	SMSLoginEnabled bool   `json:"smsLoginEnabled"`
	Version         int64  `json:"version"`
}

func (s *Service) ContactSettings(ctx context.Context) (ContactSettings, error) {
	var v ContactSettings
	e := s.db.Row(ctx, `SELECT coalesce(email_channel::text,''),coalesce(sms_channel::text,''),recovery_enabled,sms_login_enabled,version FROM identity_contact_settings WHERE singleton=1`).Scan(&v.EmailChannel, &v.SMSChannel, &v.RecoveryEnabled, &v.SMSLoginEnabled, &v.Version)
	return v, e
}
func (s *Service) SaveContactSettings(ctx context.Context, v ContactSettings) error {
	if v.SMSLoginEnabled && v.SMSChannel == "" || v.RecoveryEnabled && v.EmailChannel == "" && v.SMSChannel == "" {
		return fault.Invalid
	}
	for _, pair := range []struct{ ID, Kind string }{{v.EmailChannel, "smtp"}, {v.SMSChannel, "sms-webhook"}} {
		if pair.ID == "" {
			continue
		}
		var ok bool
		if e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM notification_channels WHERE id=$1 AND type=$2 AND enabled)`, pair.ID, pair.Kind).Scan(&ok); e != nil || !ok {
			return fault.Invalid
		}
	}

	tag, e := s.db.Exec(ctx, `UPDATE identity_contact_settings SET email_channel=nullif($1,'')::uuid,sms_channel=nullif($2,'')::uuid,recovery_enabled=$3,sms_login_enabled=$4,version=version+1 WHERE singleton=1 AND version=$5`, v.EmailChannel, v.SMSChannel, v.RecoveryEnabled, v.SMSLoginEnabled, v.Version)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}

var contactPhonePattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

func NormalizeContact(kind, value string) (string, error) {
	value = strings.TrimSpace(value)
	if kind == "email" {
		a, e := mail.ParseAddress(value)
		if e != nil || a.Address != value || len(value) > 254 || strings.ContainsAny(value, "\r\n") {
			return "", fault.Invalid
		}
		return strings.ToLower(value), nil
	}
	if kind == "phone" && contactPhonePattern.MatchString(value) {
		return value, nil
	}
	return "", fault.Invalid
}

type Contact struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Address    string    `json:"address"`
	Version    int64     `json:"version"`
	VerifiedAt time.Time `json:"verifiedAt"`
}

func (s *Service) Contacts(ctx context.Context, user string) ([]Contact, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,kind,address,version,verified_at FROM verified_contacts WHERE user_id=$1 ORDER BY kind`, user)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Contact{}
	for rows.Next() {
		var v Contact
		if e = rows.Scan(&v.ID, &v.Kind, &v.Address, &v.Version, &v.VerifiedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) ContactUser(ctx context.Context, kind, address string) (string, error) {
	var id string
	e := s.db.Row(ctx, `SELECT c.user_id FROM verified_contacts c JOIN users u ON u.id=c.user_id WHERE c.kind=$1 AND c.address=$2 AND u.status='enabled' AND u.deleted_at IS NULL`, kind, address).Scan(&id)
	return id, e
}
func (s *Service) contactDigest(token, code string) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte("contact:" + token + ":" + code))
	return hex.EncodeToString(h.Sum(nil))
}

type ContactChallenge struct {
	ChannelID       string
	ChannelVersion  int64
	UserID          string
	UserVersion     int64
	AuthEpoch       int64
	SettingsVersion int64
	Kind            string
	Address         string
	Purpose         string
}
type ContactDelivery struct {
	ID             string
	ChannelID      string
	ChannelVersion int64
	Lease          string
	Address        string
	Body           string
}

func (s *Service) StartContactChallenge(ctx context.Context, user, binding, kind, address, purpose string) (string, error) {
	address, e := NormalizeContact(kind, address)
	if e != nil {
		return "", e
	}
	if binding == "" || purpose != "contact" && purpose != "recovery" && purpose != "sms-login" {
		return "", fault.Invalid
	}
	settings, e := s.ContactSettings(ctx)
	if e != nil {
		return "", e
	}
	channel := settings.EmailChannel
	if kind == "phone" {
		channel = settings.SMSChannel
	}
	if channel == "" || purpose == "recovery" && !settings.RecoveryEnabled || purpose == "sms-login" && (!settings.SMSLoginEnabled || kind != "phone") {
		return "", fault.Proof
	}
	if e = s.LockUser(ctx, user); e != nil {
		return "", e
	}
	var recent bool
	if e = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM contact_challenges WHERE user_id=$1 AND created_at>now()-interval '60 seconds')`, user).Scan(&recent); e != nil {
		return "", e
	}
	if recent {
		return "", fault.Conflict
	}
	var version, epoch, channelVersion int64
	if e = s.db.Row(ctx, `SELECT u.version,i.auth_epoch,c.version FROM users u CROSS JOIN instance_state i CROSS JOIN notification_channels c WHERE u.id=$1 AND u.status='enabled' AND u.deleted_at IS NULL AND c.id=$2 AND c.enabled`, user, channel).Scan(&version, &epoch, &channelVersion); e != nil {
		return "", fault.Proof
	}
	token := NewToken()
	n, e := rand.Int(rand.Reader, big.NewInt(1000000))
	if e != nil {
		return "", e
	}
	code := fmt.Sprintf("%06d", n.Int64())
	if _, e = s.db.Exec(ctx, `DELETE FROM contact_challenges WHERE expires_at<now()-interval '1 day'`); e != nil {
		return "", e
	}
	if _, e = s.db.Exec(ctx, `UPDATE contact_challenges SET consumed_at=now() WHERE user_id=$1 AND purpose=$2 AND consumed_at IS NULL`, user, purpose); e != nil {
		return "", e
	}
	if _, e = s.db.Exec(ctx, `INSERT INTO contact_challenges(token_hash,user_id,user_version,auth_epoch,settings_version,kind,address,purpose,binding_hash,code_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now()+interval '5 minutes')`, Digest(token), user, version, epoch, settings.Version, kind, address, purpose, Digest(binding), s.contactDigest(token, code)); e != nil {
		return "", e
	}
	payload, _ := json.Marshal(map[string]string{"address": address, "body": "Puppeteer " + purpose + " verification code: " + code + ". Valid for 5 minutes. Do not share this code."})
	defer clear(payload)
	sealed, e := secretbox.Seal(s.key, payload, "contact-delivery:"+Digest(token))
	if e != nil {
		return "", e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO contact_deliveries(challenge_hash,channel_id,channel_version,payload_sealed) VALUES($1,$2,$3,$4)`, Digest(token), channel, channelVersion, sealed)
	return token, e
}

// Wrong attempts and successful consumption are committed before the protected action.
func (s *Service) ConsumeContactChallenge(ctx context.Context, token, binding, purpose, code string) (ContactChallenge, error) {
	var out ContactChallenge
	if len(token) != 64 || len(code) != 6 || binding == "" {
		return out, fault.Proof
	}
	valid := false
	e := s.db.Within(ctx, func(ctx context.Context) error {
		var expected string
		var attempts int
		e := s.db.Row(ctx, `SELECT c.user_id,c.user_version,c.auth_epoch,c.settings_version,c.kind,c.address,c.purpose,c.code_hash,c.attempts,d.channel_id,d.channel_version FROM contact_challenges c JOIN contact_deliveries d ON d.challenge_hash=c.token_hash JOIN notification_channels n ON n.id=d.channel_id AND n.enabled AND n.version=d.channel_version JOIN instance_state i ON i.auth_epoch=c.auth_epoch JOIN identity_contact_settings s ON s.version=c.settings_version WHERE c.token_hash=$1 AND c.binding_hash=$2 AND c.purpose=$3 AND c.consumed_at IS NULL AND c.expires_at>now() AND c.attempts<5 FOR UPDATE OF c`, Digest(token), Digest(binding), purpose).Scan(&out.UserID, &out.UserVersion, &out.AuthEpoch, &out.SettingsVersion, &out.Kind, &out.Address, &out.Purpose, &expected, &attempts, &out.ChannelID, &out.ChannelVersion)
		if e != nil {
			return fault.Proof
		}
		valid = subtle.ConstantTimeCompare([]byte(expected), []byte(s.contactDigest(token, code))) == 1
		_, e = s.db.Exec(ctx, `UPDATE contact_challenges SET attempts=attempts+1,consumed_at=CASE WHEN $2 OR attempts>=4 THEN now() ELSE consumed_at END WHERE token_hash=$1`, Digest(token), valid)
		return e
	})
	if e != nil {
		return out, e
	}
	if !valid {
		return out, fault.Proof
	}
	return out, nil
}
func (s *Service) ValidateContactChallenge(ctx context.Context, c ContactChallenge) error {
	if e := s.LockUser(ctx, c.UserID); e != nil {
		return e
	}
	var channelVersion int64
	if e := s.db.Row(ctx, `SELECT version FROM notification_channels WHERE id=$1 AND enabled FOR SHARE`, c.ChannelID).Scan(&channelVersion); e != nil || channelVersion != c.ChannelVersion {
		return fault.Proof
	}

	var ok bool
	e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM users u CROSS JOIN instance_state i CROSS JOIN identity_contact_settings s WHERE u.id=$1 AND u.version=$2 AND u.status='enabled' AND u.deleted_at IS NULL AND i.auth_epoch=$3 AND s.version=$4)`, c.UserID, c.UserVersion, c.AuthEpoch, c.SettingsVersion).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return fault.Proof
	}
	if c.Purpose != "contact" {
		id, e := s.ContactUser(ctx, c.Kind, c.Address)
		if e != nil || id != c.UserID {
			return fault.Proof
		}
	}
	return nil
}
func (s *Service) SaveVerifiedContact(ctx context.Context, c ContactChallenge) error {
	if c.Purpose != "contact" {
		return fault.Proof
	}
	if e := s.ValidateContactChallenge(ctx, c); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `INSERT INTO verified_contacts(user_id,kind,address) VALUES($1,$2,$3) ON CONFLICT(user_id,kind) DO UPDATE SET address=EXCLUDED.address,version=verified_contacts.version+1,verified_at=now()`, c.UserID, c.Kind, c.Address); e != nil {
		return e
	}
	return s.invalidateOIDCBindings(ctx, c.UserID)
}
func (s *Service) RemoveContact(ctx context.Context, user, id string) error {
	tag, e := s.db.Exec(ctx, `DELETE FROM verified_contacts WHERE user_id=$1 AND id=$2`, user, id)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Denied
	}
	return s.invalidateOIDCBindings(ctx, user)
}
func (s *Service) RecoverContactPassword(ctx context.Context, c ContactChallenge, hash string) error {
	if c.Purpose != "recovery" {
		return fault.Proof
	}
	if e := s.ValidateContactChallenge(ctx, c); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `UPDATE users SET password_hash=$2,version=version+1 WHERE id=$1`, c.UserID, hash); e != nil {
		return e
	}
	if e := s.RevokeDatabasePassword(ctx, c.UserID); e != nil {
		return e
	}
	return s.RevokeAll(ctx, c.UserID)
}
func (s *Service) ContactLoginUser(ctx context.Context, c ContactChallenge) (User, error) {
	var u User
	var name string
	if e := s.db.Row(ctx, `SELECT username FROM users WHERE id=$1`, c.UserID).Scan(&name); e != nil {
		return u, e
	}
	if e := s.CheckLock(ctx, name); e != nil {
		return u, e
	}
	if c.Purpose != "sms-login" {
		return u, fault.Proof
	}
	if e := s.ValidateContactChallenge(ctx, c); e != nil {
		return u, e
	}
	e := s.db.Row(ctx, `SELECT u.id,u.username,u.nickname,u.type,v.id,v.version FROM users u JOIN verified_contacts v ON v.user_id=u.id WHERE u.id=$1 AND v.kind='phone' AND v.address=$2`, c.UserID, c.Address).Scan(&u.ID, &u.Username, &u.Nickname, &u.Type, &u.ContactID, &u.ContactVersion)
	u.ContactSettingsVersion = c.SettingsVersion
	u.ContactChannelVersion = c.ChannelVersion
	return u, e
}
func (s *Service) ClaimContactDelivery(ctx context.Context) (ContactDelivery, error) {
	var d ContactDelivery
	var sealed []byte
	var hash string
	d.Lease = NewToken()
	e := s.db.Row(ctx, `WITH candidate AS(SELECT d.id FROM contact_deliveries d JOIN contact_challenges c ON c.token_hash=d.challenge_hash JOIN instance_state i ON i.auth_epoch=c.auth_epoch JOIN identity_contact_settings s ON s.version=c.settings_version JOIN users u ON u.id=c.user_id AND u.version=c.user_version WHERE d.status IN('pending','sending') AND d.attempts<3 AND d.next_attempt<=now() AND c.consumed_at IS NULL AND c.expires_at>now() AND u.status='enabled' AND u.deleted_at IS NULL ORDER BY d.next_attempt LIMIT 1 FOR UPDATE OF d SKIP LOCKED) UPDATE contact_deliveries d SET status='sending',attempts=attempts+1,lease_token=$1,next_attempt=now()+interval '45 seconds' FROM candidate c WHERE d.id=c.id RETURNING d.id,d.channel_id,d.channel_version,d.payload_sealed,d.challenge_hash`, d.Lease).Scan(&d.ID, &d.ChannelID, &d.ChannelVersion, &sealed, &hash)
	if e != nil {
		return d, e
	}
	raw, e := secretbox.Open(s.key, sealed, "contact-delivery:"+hash)
	if e != nil {
		return d, e
	}
	defer clear(raw)
	var p map[string]string
	if e = json.Unmarshal(raw, &p); e != nil {
		return d, e
	}
	d.Address = p["address"]
	d.Body = p["body"]
	return d, nil
}
func (s *Service) FinishContactDelivery(ctx context.Context, d ContactDelivery, success bool) error {
	status, message := "pending", "delivery failed"
	if success {
		status, message = "delivered", ""
	}
	_, e := s.db.Exec(ctx, `UPDATE contact_deliveries SET status=CASE WHEN NOT $4 AND attempts>=3 THEN 'failed' ELSE $3 END,last_error=$5,next_attempt=now()+interval '20 seconds',payload_sealed=CASE WHEN $4 OR attempts>=3 THEN ''::bytea ELSE payload_sealed END WHERE id=$1 AND lease_token=$2 AND status='sending'`, d.ID, d.Lease, status, success, message)
	return e
}
