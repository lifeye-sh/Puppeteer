package application

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"next-terminal/backend/internal/modules/entitlements"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
)

func applyEntitlementCapabilities(caps map[string]any, v entitlements.Snapshot) {
	admission := map[string]bool{}
	for _, key := range entitlements.ControlledFeatures {
		admission[key] = v.Allows(key)
	}
	caps["admission"] = admission
	reason := "not_entitled"
	if v.State == "expired" {
		reason = "expired"
	} else if v.State != "valid" {
		reason = "invalid_license"
	}
	features := caps["features"].(map[string]any)
	deny := func(key string) {
		if current, ok := features[key].(map[string]any); ok && current["state"] == "available" {
			current["state"] = "not_entitled"
			current["reason"] = reason
		}
	}
	// The workspace/history/configuration remain accessible after expiry.
	for _, key := range []string{"automation.execute", "notification.send", "files.write"} {
		if !v.Allows(key) {
			deny(key)
		}
	}
	if !v.Allows("ai.assistant") {
		for _, key := range []string{"ai.assistant", "ai.inference", "ai.stream", "ai.queue", "ai.tools", "ai.models"} {
			deny(key)
		}
	}
	for _, key := range []string{"session.ssh", "session.telnet", "database.mysql", "database.postgres"} {
		if !v.Allows(key) {
			deny(key)
		}
	}
	if !v.Allows("session.ssh") {
		for _, key := range []string{"native.ssh", "native.ssh-forward", "native.https-forward"} {
			deny(key)
		}
	}
	if !v.Allows("session.rdp") {
		deny("session.rdp.native")
	}
	if !v.Allows("session.rdp") && !v.Allows("session.vnc") {
		deny("session.graphics")
	}
	for protocol, flags := range caps["protocols"].(map[string]map[string]bool) {
		if !v.Allows("session." + protocol) {
			for key := range flags {
				flags[key] = false
			}
		}
	}
}

func (a *App) entitlementStatus(w http.ResponseWriter, r *http.Request) error {
	u, e := a.require(r, "settings.read")
	if e != nil {
		return e
	}
	if u.Type != "admin" {
		return fault.Denied
	}
	v, e := a.entitlements.Status(r.Context())
	if e != nil {
		return e
	}
	writeJSON(w, 200, v)
	return nil
}
func (a *App) importEntitlement(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		backupProof
		Token    string `json:"token"`
		Revision int64  `json:"revision"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	var revision int64
	e := a.withPasswordProof(r, in.backupProof, "identity.manage", func(ctx context.Context, u *identity.User) error {
		if u.Type != "admin" {
			return fault.Denied
		}
		var e error
		revision, e = a.entitlements.Import(ctx, in.Token, u.ID, in.Revision)
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "system.license_imported", requestID(r), sourceIP(r), map[string]string{"revision": strconv.FormatInt(revision, 10), "digest": fmt.Sprintf("%x", sha256.Sum256([]byte(in.Token)))})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"revision": revision})
	return nil
}
