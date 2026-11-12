package identity

import (
	"context"
	"errors"
	"net/netip"
	"next-terminal/backend/internal/platform/fault"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type NativeDeviceInfo struct {
	Name          string `json:"name"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	ClientVersion string `json:"clientVersion"`
}

var nativeDeviceIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_.+\-]{1,40}$`)

func (v *NativeDeviceInfo) Normalize() error {
	v.Name = strings.TrimSpace(v.Name)
	if v.Name == "" {
		v.Name = "Puppeteer 客户端"
	}
	if !utf8.ValidString(v.Name) || utf8.RuneCountInString(v.Name) > 80 {
		return fault.Invalid
	}
	for _, r := range v.Name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return fault.Invalid
		}
	}
	if v.OS == "" {
		v.OS = "unknown"
	}
	if v.Arch == "" {
		v.Arch = "unknown"
	}
	if v.ClientVersion == "" {
		v.ClientVersion = "unknown"
	}
	for _, s := range []string{v.OS, v.Arch, v.ClientVersion} {
		if !nativeDeviceIdentifier.MatchString(s) {
			return fault.Invalid
		}
	}
	return nil
}

// Called only during initial code exchange, never on refresh. Metadata is a
// display hint supplied by the client, not authentication or hardware identity.
func (s *Service) DescribeNativeDevice(ctx context.Context, g OAuthGrant, v NativeDeviceInfo, ip string) error {
	if g.ClientID != NativeClientUUID || g.UserID == "" {
		return fault.Denied
	}
	if e := v.Normalize(); e != nil {
		return e
	}
	if address, e := netip.ParseAddr(ip); e == nil {
		ip = address.Unmap().String()
	} else {
		ip = ""
	}
	tag, e := s.db.Exec(ctx, `UPDATE native_client_devices SET name=$2,os=$3,arch=$4,client_version=$5,source_ip=$6 WHERE family_id=$1`, g.ID, v.Name, v.OS, v.Arch, v.ClientVersion, ip)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Denied
	}
	return nil
}

type NativeDevice struct {
	ID string `json:"id"`
	NativeDeviceInfo
	SourceIP     string     `json:"sourceIp"`
	RegisteredAt time.Time  `json:"registeredAt"`
	LastSeenAt   *time.Time `json:"lastSeenAt,omitempty"`
	ExpiresAt    time.Time  `json:"expiresAt"`
	RevokedAt    *time.Time `json:"revokedAt,omitempty"`
	Scopes       []string   `json:"scopes"`
	Status       string     `json:"status"`
}

func (s *Service) NativeDevices(ctx context.Context, user string, offset, limit int) ([]NativeDevice, int, error) {
	items := []NativeDevice{}
	grants := []OAuthGrant{}
	var total int
	if e := s.db.Row(ctx, `SELECT count(*) FROM native_client_devices d JOIN oauth_token_families f ON f.id=d.family_id WHERE f.user_id=$1 AND f.client_id=$2`, user, NativeClientUUID).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := s.db.Rows(ctx, `SELECT f.id,d.name,d.os,d.arch,d.client_version,d.source_ip,d.registered_at,d.last_seen_at,f.expires_at,f.revoked_at,f.scopes,f.client_id,f.client_version,f.server_version,f.auth_epoch,f.user_id,f.user_version,f.consent_id,f.consent_version
 FROM native_client_devices d JOIN oauth_token_families f ON f.id=d.family_id WHERE f.user_id=$1 AND f.client_id=$2 ORDER BY d.registered_at DESC,f.id LIMIT $3 OFFSET $4`, user, NativeClientUUID, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	for rows.Next() {
		var v NativeDevice
		var g OAuthGrant
		if e = rows.Scan(&v.ID, &v.Name, &v.OS, &v.Arch, &v.ClientVersion, &v.SourceIP, &v.RegisteredAt, &v.LastSeenAt, &v.ExpiresAt, &v.RevokedAt, &v.Scopes, &g.ClientID, &g.ClientVersion, &g.ServerVersion, &g.AuthEpoch, &g.UserID, &g.UserVersion, &g.ConsentID, &g.ConsentVersion); e != nil {
			rows.Close()
			return nil, 0, e
		}
		g.ID = v.ID
		g.Scopes = v.Scopes
		items = append(items, v)
		grants = append(grants, g)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, 0, e
	}
	// Reuse the complete authorization checks (including client user allowlists)
	// instead of calling an unrevoked-but-stale family "online" or "authorized".
	for i := range items {
		v := &items[i]
		v.Status = "active"
		if v.RevokedAt != nil {
			v.Status = "revoked"
		} else if !v.ExpiresAt.After(time.Now()) {
			v.Status = "expired"
		} else if _, _, e := s.ValidateOAuthGrant(ctx, grants[i]); e != nil {
			if !errors.Is(e, fault.Proof) && !errors.Is(e, fault.Denied) {
				return nil, 0, e
			}
			v.Status = "invalid"
		}
	}
	return items, total, nil
}

func (s *Service) RevokeNativeDevice(ctx context.Context, user, id string) (bool, error) {
	if e := s.LockUser(ctx, user); e != nil {
		return false, e
	}
	var revoked *time.Time
	e := s.db.Row(ctx, `SELECT f.revoked_at FROM oauth_token_families f JOIN native_client_devices d ON d.family_id=f.id WHERE f.id=$1 AND f.user_id=$2 AND f.client_id=$3 FOR UPDATE OF f`, id, user, NativeClientUUID).Scan(&revoked)
	if e != nil {
		return false, fault.Denied
	}
	if revoked != nil {
		return false, nil
	}
	_, e = s.db.Exec(ctx, `UPDATE oauth_token_families SET revoked_at=now() WHERE id=$1`, id)
	return e == nil, e
}
