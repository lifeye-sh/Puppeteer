package application

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"strings"
)

// Issue a single-use recording-only desktop file through the native OAuth flow.
func (a *App) nativeRDPFile(w http.ResponseWriter, r *http.Request) error {
	bearer, err := nativeBearer(r)
	if err != nil {
		return err
	}
	var in struct {
		AssetID string `json:"assetId"`
	}
	if err = decode(w, r, &in); err != nil {
		return err
	}
	if !resource.ValidID(in.AssetID) {
		return fault.Invalid
	}
	var ticket, id, endpoint string
	err = a.db.Within(r.Context(), func(ctx context.Context) error {
		user, err := a.identity.NativeUser(ctx, bearer, "sessions:connect")
		if err != nil {
			return err
		}
		family, _, err := a.identity.OAuthToken(ctx, bearer, "access", identity.NativeClientUUID, false)
		if err != nil {
			return err
		}
		asset, err := a.sessionAsset(ctx, user, in.AssetID)
		if err != nil {
			return err
		}
		if asset.Protocol != "rdp" {
			return fault.Invalid
		}
		if err = a.enforcePolicy(ctx, user, asset.ID, sourceIP(r), "connect", ""); err != nil {
			return err
		}
		addr := a.nativeRDPAddress.Load()
		if addr == nil {
			return problem(409, "entry_unavailable", "原生 RDP 录屏网关尚未就绪")
		}
		endpoint = *addr
		pin, err := hex.DecodeString(strings.TrimPrefix(asset.CertFingerprint, "sha256:"))
		if err != nil || len(pin) != 32 || !strings.HasPrefix(asset.CertFingerprint, "sha256:") {
			return fault.Conflict
		}
		if !a.allowAttempt("rdp-create:" + user.ID) {
			return problem(429, "rate_limited", "会话创建过于频繁")
		}
		cred, err := a.vault.Get(ctx, asset.CredentialID)
		if err != nil {
			return err
		}
		id, err = a.sessions.Reserve(ctx, session.Session{ActorID: user.ID, AssetID: asset.ID, Protocol: "rdp", AssetVersion: asset.Version, CredentialVersion: cred.Version, Readonly: true, ClientIP: sourceIP(r)}, "", identity.Digest(family.ID))
		if err != nil {
			return err
		}
		ticket = "NTICKET:" + id + ":" + identity.NewToken()
		if err = a.sessions.IssueNativeRDPTicket(ctx, id, a.nativeRDPTicketHash(ticket), family.ID); err != nil {
			return err
		}
		if err = a.audit.OpenAccess(ctx, id, user.ID, asset.ID, user.Username, asset.Name, net.JoinHostPort(asset.Host, strconv.Itoa(asset.Port)), sourceIP(r)); err != nil {
			return err
		}
		return a.audit.Detail(ctx, user.ID, "native.rdp_reserved", requestID(r), sourceIP(r), map[string]string{"sessionId": id, "assetId": asset.ID})
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "view-"+id+".rdp"))
	_, err = fmt.Fprintf(w, "full address:s:%s\r\nusername:s:%s\r\nauthentication level:i:1\r\nenablecredsspsupport:i:0\r\nnegotiate security layer:i:1\r\nscreen mode id:i:1\r\ndesktopwidth:i:1280\r\ndesktopheight:i:720\r\nredirectclipboard:i:0\r\ndrivestoredirect:s:\r\nredirectprinters:i:0\r\nredirectsmartcards:i:0\r\naudiomode:i:2\r\n", endpoint, ticket)
	return err
}
