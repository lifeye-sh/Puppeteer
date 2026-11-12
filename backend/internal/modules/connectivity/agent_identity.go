package connectivity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/netip"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"time"
)

type NetworkScope struct {
	CIDRs []string `json:"cidrs"`
	Ports []int    `json:"ports"`
}

func (v NetworkScope) Validate() error {
	if len(v.CIDRs) == 0 || len(v.CIDRs) > 64 || len(v.Ports) == 0 || len(v.Ports) > 64 {
		return fault.Invalid
	}
	for _, c := range v.CIDRs {
		p, e := netip.ParsePrefix(c)
		if e != nil || p != p.Masked() || p.Addr().Is4In6() {
			return fault.Invalid
		}
	}
	for _, p := range v.Ports {
		if p < 1 || p > 65535 {
			return fault.Invalid
		}
	}
	return nil
}
func deviceSecret() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func deviceDigest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

type Registration struct {
	ID         string       `json:"id"`
	Token      string       `json:"token,omitempty"`
	Name       string       `json:"name"`
	Scope      NetworkScope `json:"networkScope"`
	ExpiresAt  time.Time    `json:"expiresAt"`
	ConsumedAt *time.Time   `json:"consumedAt"`
	RevokedAt  *time.Time   `json:"revokedAt"`
}

func (s *Service) CreateRegistration(ctx context.Context, name, actor string, scope NetworkScope) (v Registration, e error) {
	if strings.TrimSpace(name) == "" || len(name) > 200 {
		return v, fault.Invalid
	}
	if e = scope.Validate(); e != nil {
		return
	}
	v.Token = deviceSecret()
	v.Name = name
	v.Scope = scope
	raw, _ := json.Marshal(scope)
	e = s.db.Row(ctx, `INSERT INTO agent_registration_tokens(token_hash,name,network_scope,created_by) VALUES($1,$2,$3,$4) RETURNING id,expires_at`, deviceDigest(v.Token), name, raw, actor).Scan(&v.ID, &v.ExpiresAt)
	return
}
func (s *Service) Registrations(ctx context.Context) ([]Registration, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,name,network_scope,expires_at,consumed_at,revoked_at FROM agent_registration_tokens ORDER BY created_at DESC LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Registration{}
	for rows.Next() {
		var v Registration
		var raw []byte
		if e = rows.Scan(&v.ID, &v.Name, &raw, &v.ExpiresAt, &v.ConsumedAt, &v.RevokedAt); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &v.Scope); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type Device struct {
	DialReady       bool         `json:"dialReady"`
	Online          bool         `json:"online"`
	LastHeartbeatAt *time.Time   `json:"lastHeartbeatAt"`
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	OS              string       `json:"os"`
	Arch            string       `json:"arch"`
	Version         string       `json:"version"`
	Scope           NetworkScope `json:"networkScope"`
	RevokedAt       *time.Time   `json:"revokedAt"`
}
type Enrollment struct {
	RegistrationToken string `json:"registrationToken"`
	Name              string `json:"name"`
	OS                string `json:"os"`
	Arch              string `json:"arch"`
	Version           string `json:"version"`
}
type Enrolled struct {
	AgentID           string       `json:"agentId"`
	DeviceKey         string       `json:"deviceKey"`
	CredentialVersion int          `json:"credentialVersion"`
	Scope             NetworkScope `json:"networkScope"`
	ProtocolVersion   string       `json:"protocolVersion"`
}

// Caller supplies the transaction so token consumption, identity and audit commit together.
func (s *Service) Enroll(ctx context.Context, in Enrollment) (v Enrolled, e error) {
	if len(in.RegistrationToken) != 64 || strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 || len(in.OS) == 0 || len(in.OS) > 32 || len(in.Arch) == 0 || len(in.Arch) > 32 || len(in.Version) == 0 || len(in.Version) > 64 {
		return v, fault.Invalid
	}
	var id string
	var raw []byte
	e = s.db.Row(ctx, `UPDATE agent_registration_tokens SET consumed_at=now() WHERE token_hash=$1 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>now() RETURNING id,network_scope`, deviceDigest(in.RegistrationToken)).Scan(&id, &raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, fault.Denied
	}
	if e != nil {
		return
	}
	if e = json.Unmarshal(raw, &v.Scope); e != nil {
		return
	}
	v.DeviceKey = deviceSecret()
	v.CredentialVersion = 1
	v.ProtocolVersion = "nt-agent.v1"
	e = s.db.Row(ctx, `INSERT INTO agent_gateway_configs(registration_id,name,credential_hash,os,arch,version,network_scope) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, id, in.Name, deviceDigest(v.DeviceKey), in.OS, in.Arch, in.Version, raw).Scan(&v.AgentID)
	return
}
func (s *Service) Devices(ctx context.Context) ([]Device, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,name,os,arch,version,network_scope,revoked_at,last_heartbeat_at FROM agent_gateway_configs ORDER BY created_at DESC LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var v Device
		var raw []byte
		if e = rows.Scan(&v.ID, &v.Name, &v.OS, &v.Arch, &v.Version, &raw, &v.RevokedAt, &v.LastHeartbeatAt); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &v.Scope); e != nil {
			return nil, e
		}
		v.Online = v.RevokedAt == nil && s.AgentOnline(v.ID)
		v.DialReady = v.RevokedAt == nil && s.AgentDialReady(v.ID)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) RevokeDevice(ctx context.Context, id string) error {
	tag, e := s.db.Exec(ctx, `UPDATE agent_gateway_configs SET revoked_at=COALESCE(revoked_at,now()),connection_epoch=connection_epoch+1 WHERE id=$1`, id)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Invalid
	}
	return e
}
func (s *Service) RevokeRegistration(ctx context.Context, id string) error {
	tag, e := s.db.Exec(ctx, `UPDATE agent_registration_tokens SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1`, id)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Invalid
	}
	return e
}
func (s *Service) AuthenticateDevice(ctx context.Context, key string) (string, error) {
	if len(key) != 64 {
		return "", fault.Denied
	}
	var id string
	e := s.db.Row(ctx, `SELECT id FROM agent_gateway_configs WHERE credential_hash=$1 AND revoked_at IS NULL`, deviceDigest(key)).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		e = fault.Denied
	}
	return id, e
}

func (s *Service) ValidateDevice(ctx context.Context, id string) error {
	var valid bool
	e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM agent_gateway_configs WHERE id=$1 AND revoked_at IS NULL)`, id).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return fault.Denied
	}
	return nil
}
