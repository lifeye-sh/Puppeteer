package application

import (
	"context"
	"golang.org/x/crypto/ssh"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/automation"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/vault"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/jobs"
	"strings"
	"time"
)

func (a *App) reconcileRotation(w http.ResponseWriter, r *http.Request) error {
	u, e := a.require(r, "credential.rotate")
	if e != nil {
		return e
	}
	id, targetID := r.PathValue("id"), r.PathValue("target")
	if !resource.ValidID(id) || !resource.ValidID(targetID) {
		return fault.Invalid
	}
	select {
	case a.probeSlots <- struct{}{}:
		defer func() { <-a.probeSlots }()
	default:
		return problem(429, "probe_busy", "核对繁忙，请稍后重试")
	}
	ctx, stop := context.WithTimeout(r.Context(), 30*time.Second)
	defer stop()
	run, e := a.vault.RotationRun(ctx, id)
	if e != nil {
		return e
	}
	if run.ActorID != u.ID && u.Type != "admin" {
		return fault.Denied
	}
	// An interactive probe uses the caller's current network location, not the
	// source address captured by an earlier manual or scheduled run.
	run.SourceIP = sourceIP(r)
	var target vault.RotationTarget
	for _, t := range run.Targets {
		if t.ID == targetID {
			target = t
		}
	}
	if target.ID == "" {
		return fault.Invalid
	}
	result := vault.RotationUnknown
	e = a.db.Within(ctx, func(c context.Context) error {
		if e := a.vault.LockReconcile(c, id, targetID); e != nil {
			return e
		}
		asset, e := a.rotationAsset(c, run, target)
		if e != nil {
			return e
		}
		trust, e := a.connectivity.Get(c, asset.ID)
		if e != nil {
			return e
		}
		if trust.Host != asset.Host || trust.Port != asset.Port {
			return fault.Conflict
		}
		cred, old, e := a.vault.RotationAuth(c, target.CredentialID, u.ID, id)
		if e != nil {
			return e
		}
		if trust.Fingerprint+":"+strings.ToLower(cred.Username) != target.AccountKey {
			return fault.Conflict
		}
		candidate, e := a.vault.RotationSecret(c, targetID, u.ID, id)
		if e != nil {
			return e
		}
		next, e := rotationMethod(target.Kind, candidate)
		if e != nil {
			return e
		}
		authorize := func(c context.Context) error { _, e := a.rotationAsset(c, run, target); return e }
		var transport connectivity.Dialer
		switch {
		case asset.AgentID != "":
			transport = func(dc context.Context, h string, p int) (net.Conn, error) {
				return a.connectivity.DialAgent(ctx, dc, asset.AgentID, id, h, p, authorize)
			}
		case asset.JumpID != "":
			transport = a.jumpDialer(c, asset.JumpID, u.ID, id)
		case asset.GatewayGroupID != "":
			transport = a.groupDialer(c, asset.GatewayGroupID, u.ID, id, authorize)
		case asset.ProxyID != "":
			transport = a.proxyDialer(c, asset.ProxyID, u.ID, id)
		}
		probe := func(method ssh.AuthMethod) (bool, bool) {
			dc, cancel := context.WithTimeout(c, 10*time.Second)
			defer cancel()
			client, e := connectivity.ConnectUsing(dc, trust, cred.Username, method, transport)
			if e == nil {
				client.Close()
				return true, true
			}
			return false, strings.Contains(e.Error(), "unable to authenticate")
		}
		newWorks, newCertain := probe(next)
		oldWorks, oldCertain := probe(old)
		if newCertain && oldCertain {
			result = vault.ReconciledRotation(newWorks, oldWorks)
		}
		if result == vault.RotationSucceeded {
			latestTrust, e := a.connectivity.Get(c, asset.ID)
			if e != nil {
				return e
			}
			if latestTrust.Version != trust.Version {
				return fault.Conflict
			}
			cid, e := a.vault.ActivateRotation(c, target, cred.Username, candidate)
			if e != nil {
				return e
			}
			asset.CredentialID = cid
			if _, e = a.resources.Save(c, asset); e != nil {
				return e
			}
		}
		if result != vault.RotationUnknown {
			if e = a.vault.ResolveRotation(c, id, targetID, result); e != nil {
				return e
			}
			targets, e := a.vault.RotationTargets(c, id)
			if e != nil {
				return e
			}
			success, unknown := 0, false
			for _, t := range targets {
				if t.State == vault.RotationSucceeded {
					success++
				}
				if t.State == vault.RotationUnknown {
					unknown = true
				}
			}
			state := "failed"
			if success == len(targets) {
				state = "succeeded"
			} else if success > 0 {
				state = "partial"
			}
			if unknown {
				state = "unknown"
			}
			if e = jobs.New(a.db).Reconcile(c, id, state); e != nil {
				return e
			}
			if e = automation.New(a.db).ReconcileTarget(c, id, target.AssetID, string(result)); e != nil {
				return e
			}
		}
		return a.audit.Detail(c, u.ID, "rotation.reconciled", requestID(r), sourceIP(r), map[string]string{"id": id, "targetId": targetID, "state": string(result)})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"state": result})
	return nil
}
