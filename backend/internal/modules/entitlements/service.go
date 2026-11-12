package entitlements

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"time"
)

var ErrQuota = errors.New("entitlement quota exhausted")
var ErrDenied = errors.New("capability not entitled")

// ControlledFeatures is the admission scope currently enforced by the service.
// Essential login, recovery, audit and deletion remain accessible on expiry.
var ControlledFeatures = []string{"identity.users", "resource.assets", "session.ssh", "session.telnet", "session.rdp", "session.vnc", "database.mysql", "database.postgres", "ai.assistant", "automation.execute", "notification.send", "files.write"}

type Service struct {
	db       *postgres.DB
	local    CapabilityProvider
	verifier *Verifier
}

func NewService(db *postgres.DB, local CapabilityProvider, verifier *Verifier) *Service {
	if local == nil {
		local, _ = NewLocal(nil)
	}
	return &Service{db: db, local: local, verifier: verifier}
}

type Status struct {
	Snapshot
	LicenseRevision    int64            `json:"licenseRevision"`
	Used               map[string]int64 `json:"used"`
	ControlledFeatures []string         `json:"controlledFeatures"`
	ImportAllowed      bool             `json:"importAllowed"`
}

func (s *Service) Snapshot(ctx context.Context) (Snapshot, int64, error) {
	var instance string
	if e := s.db.Row(ctx, `SELECT instance_id FROM instance_state WHERE singleton=1`).Scan(&instance); e != nil {
		return Snapshot{}, 0, e
	}
	if s.verifier == nil {
		v, e := s.local.Snapshot(instance, time.Now())
		return v, 0, e
	}
	var revision int64
	var token string
	if e := s.db.Row(ctx, `SELECT coalesce(h.revision,0),coalesce(l.token,'') FROM entitlement_head h LEFT JOIN entitlement_licenses l ON l.revision=h.revision WHERE h.singleton=1`).Scan(&revision, &token); e != nil {
		return Snapshot{}, 0, e
	}
	if token == "" {
		return Snapshot{Source: "signed", InstanceID: instance, State: "missing", Features: map[string]bool{}}, revision, ErrInvalid
	}
	p, e := NewSigned(s.verifier, token)
	if e != nil {
		return Snapshot{}, revision, e
	}
	v, e := p.Snapshot(instance, time.Now())
	return v, revision, e
}

func (s *Service) Usage(ctx context.Context) (map[string]int64, error) {
	var users, resources, sessions int64
	e := s.db.Row(ctx, `SELECT (SELECT count(*) FROM users WHERE deleted_at IS NULL),
 (SELECT count(*) FROM resource_registry WHERE deleted_at IS NULL AND kind IN ('asset','database','website')),
 (SELECT count(*) FROM sessions WHERE closed_at IS NULL AND lease_expires_at>clock_timestamp() AND quota_epoch=i.auth_epoch)+
 (SELECT count(*) FROM database_sessions WHERE ended_at IS NULL AND quota_lease_until>clock_timestamp() AND quota_epoch=i.auth_epoch)
 FROM instance_state i WHERE singleton=1`).Scan(&users, &resources, &sessions)
	return map[string]int64{"users": users, "resources": resources, "sessions": sessions}, e
}
func (s *Service) Status(ctx context.Context) (Status, error) {
	v, rev, e := s.Snapshot(ctx)
	if e != nil && !errors.Is(e, ErrInvalid) && !errors.Is(e, ErrExpired) && !errors.Is(e, ErrNotYetValid) {
		return Status{}, e
	}
	used, e := s.Usage(ctx)
	return Status{Snapshot: v, LicenseRevision: rev, Used: used, ControlledFeatures: append([]string(nil), ControlledFeatures...), ImportAllowed: s.verifier != nil}, e
}

// Admit and insertion must share a transaction. Transaction-scoped, per-category
// locks coordinate all application nodes and all resource/session protocols.
func (s *Service) Admit(ctx context.Context, category, feature string) error {
	if category != "" {
		if !s.db.InTransaction(ctx) {
			return errors.New("entitlement admission requires transaction")
		}
		key := int64(700001)
		switch category {
		case "users":
		case "resources":
			key++
		case "sessions", "sessions.renew":
			key += 2
		default:
			return fault.Invalid
		}
		if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, key); e != nil {
			return e
		}
	}
	v, _, e := s.Snapshot(ctx)
	if e != nil {
		return e
	}
	if !v.Allows(feature) {
		return ErrDenied
	}
	if category == "" || category == "sessions.renew" {
		return nil
	}
	var limit *int64
	switch category {
	case "users":
		limit = v.Quotas.Users
	case "resources":
		limit = v.Quotas.Resources
	case "sessions":
		limit = v.Quotas.Sessions
	}
	if limit == nil {
		return nil
	}
	used, e := s.Usage(ctx)
	if e != nil {
		return e
	}
	if used[category] >= *limit {
		return fmt.Errorf("%w: %s", ErrQuota, category)
	}
	return nil
}

// Import validates against the instance and immutable trust store. It never
// accepts a public key or provider-mode change supplied by the API caller.
func (s *Service) Import(ctx context.Context, token, actor string, expected int64) (int64, error) {
	if !s.db.InTransaction(ctx) {
		return 0, errors.New("license import requires transaction")
	}
	if s.verifier == nil {
		return 0, ErrDenied
	}
	var instance string
	if e := s.db.Row(ctx, `SELECT instance_id FROM instance_state WHERE singleton=1`).Scan(&instance); e != nil {
		return 0, e
	}
	c, e := s.verifier.Verify(token, instance, time.Now())
	if e != nil {
		return 0, e
	}
	var revision int64
	if e = s.db.Row(ctx, `SELECT coalesce(revision,0) FROM entitlement_head WHERE singleton=1 FOR UPDATE`).Scan(&revision); e != nil {
		return 0, e
	}
	if revision != expected {
		return 0, fault.Conflict
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
	var old string
	if e = s.db.Row(ctx, `SELECT coalesce((SELECT digest FROM entitlement_licenses WHERE revision=$1),'')`, revision).Scan(&old); e != nil {
		return 0, e
	}
	if old == digest {
		return revision, nil
	}
	if e = s.db.Row(ctx, `INSERT INTO entitlement_licenses(instance_id,license_id,token,digest,actor_id) VALUES($1,$2,$3,$4,$5) RETURNING revision`, instance, c.LicenseID, token, digest, actor).Scan(&revision); e != nil {
		return 0, e
	}
	_, e = s.db.Exec(ctx, `UPDATE entitlement_head SET revision=$1 WHERE singleton=1`, revision)
	return revision, e
}
