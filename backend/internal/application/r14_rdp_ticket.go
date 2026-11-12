package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"time"
)

func (a *App) nativeRDPTicketHash(ticket string) string {
	mac := hmac.New(sha256.New, a.config.EncryptionKey)
	mac.Write([]byte("native_rdp_ticket:v1:" + ticket))
	return hex.EncodeToString(mac.Sum(nil))
}

// Only the authenticated worker control backend may call this method. Worker ID
// and source IP must come from the supervisor and accepted socket, not RPC input.
// This commits before any target preparation or credential disclosure. There is
// deliberately no public issuing endpoint until the complete RDP path is ready.
func (a *App) consumeNativeRDPTicket(ctx context.Context, ticket, worker, clientIP string) (session.Session, string, error) {
	var v session.Session
	parts := strings.Split(ticket, ":")
	if len(parts) != 3 || parts[0] != "NTICKET" || !resource.ValidID(parts[1]) || !resource.ValidID(worker) || len(parts[2]) != 64 {
		return v, "", fault.Proof
	}
	raw, e := hex.DecodeString(parts[2])
	if e != nil || hex.EncodeToString(raw) != parts[2] {
		return v, "", fault.Proof
	}
	var family string
	digest := a.nativeRDPTicketHash(ticket)
	if e = a.db.Row(ctx, `SELECT family_id FROM native_rdp_tickets WHERE session_id=$1 AND ticket_hash=$2 AND consumed_at IS NULL AND expires_at>now()`, parts[1], digest).Scan(&family); e != nil {
		return v, "", fault.Proof
	}
	e = a.db.Within(ctx, func(ctx context.Context) error {
		u, e := a.identity.NativeFamilyUser(ctx, family)
		if e != nil {
			return e
		}
		v, e = a.sessions.Get(ctx, parts[1])
		if e != nil {
			return e
		}
		if v.Protocol != "rdp" || v.ClientIP != clientIP {
			return fault.Denied
		}
		if _, e = a.checkLiveUser(ctx, v, u); e != nil {
			return e
		}
		v, e = a.sessions.ConsumeNativeRDPTicket(ctx, v.ID, u.ID, digest, worker)
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "native.rdp_ticket_consumed", "", clientIP, map[string]string{"sessionId": v.ID, "assetId": v.AssetID, "workerId": worker})
	})
	if e != nil {
		return session.Session{}, "", e
	}
	return v, family, nil
}

// Renew only the session owned by this worker. Every lease rechecks the original
// OAuth family and current policy; logging out/revoking authorization stops it.
func (a *App) renewNativeRDPSession(ctx context.Context, id, worker string) (time.Time, error) {
	var expiry time.Time
	if !resource.ValidID(id) || !resource.ValidID(worker) {
		return expiry, fault.Denied
	}
	e := a.db.Within(ctx, func(ctx context.Context) error {
		var family string
		if e := a.db.Row(ctx, `SELECT family_id FROM native_rdp_tickets WHERE session_id=$1 AND worker_id=$2 AND consumed_at IS NOT NULL`, id, worker).Scan(&family); e != nil {
			return fault.Denied
		}
		u, e := a.identity.NativeFamilyUser(ctx, family)
		if e != nil {
			return e
		}
		v, e := a.sessions.Get(ctx, id)
		if e != nil {
			return e
		}
		if v.Protocol != "rdp" {
			return fault.Denied
		}
		if _, e = a.checkLiveUser(ctx, v, u); e != nil {
			return e
		}
		if e = a.sessions.Renew(ctx, id); e != nil {
			return e
		}
		return a.db.Row(ctx, `SELECT lease_expires_at FROM sessions WHERE id=$1`, id).Scan(&expiry)
	})
	return expiry, e
}
