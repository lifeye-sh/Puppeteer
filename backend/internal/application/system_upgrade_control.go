package application

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"puppeteer/backend/internal/modules/identity"
	"puppeteer/backend/internal/platform/fault"
)

// These loopback-only controls authenticate both the enrolled host and the
// original administrator again immediately before draining a running instance.
func (a *App) registerUpgradeControl(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	for _, operation := range []string{"check", "drain", "resume"} {
		h("POST /api/internal/deployment-upgrade-"+operation, func(w http.ResponseWriter, r *http.Request) error {
			if !internalDeploymentRequest(r) {
				return fault.Denied
			}
			raw, e := os.ReadFile(a.config.DeployTokenFile)
			credential := strings.TrimSpace(string(raw))
			expected := sha256.Sum256([]byte(credential))
			supplied := sha256.Sum256([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
			if e != nil || len(credential) < 32 || subtle.ConstantTimeCompare(expected[:], supplied[:]) != 1 {
				return fault.Denied
			}
			var in struct {
				Actor string `json:"actor"`
			}
			if e = decode(w, r, &in); e != nil {
				return e
			}
			var actor identity.User
			if e = a.db.Row(r.Context(), `SELECT id,type FROM users WHERE id=$1 AND status='enabled' AND deleted_at IS NULL`, in.Actor).Scan(&actor.ID, &actor.Type); e != nil || actor.Type != "admin" {
				return fault.Denied
			}
			if e = a.allowed(r.Context(), &actor, "identity.manage"); e != nil {
				return e
			}
			a.upgradeMu.Lock()
			defer a.upgradeMu.Unlock()
			if operation != "check" {
				if e = a.audit.Detail(r.Context(), actor.ID, "system.update."+operation, requestID(r), sourceIP(r), map[string]string{"source": "host-executor"}); e != nil {
					return e
				}
			}
			if operation == "resume" {
				if a.upgradeRelease != nil {
					a.upgradeRelease()
					a.upgradeRelease = nil
				}
			} else if a.upgradeRelease == nil {
				e = a.upgradeApplicationIdle(r.Context())
				if e == nil && operation == "drain" {
					if e = a.drainHeavyForDeployment(); e == nil {
						ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
						leave, err := a.beginMaintenance(ctx)
						cancel()
						if err != nil {
							a.resumeHeavyAfterDeployment()
							e = err
						} else {
							// A permission change already in flight can finish while
							// requests drain. Recheck under the exclusive request gate.
							var valid bool
							if e = a.db.Row(r.Context(), `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND type='admin' AND status='enabled' AND deleted_at IS NULL)`, actor.ID).Scan(&valid); e != nil || !valid {
								leave()
								a.resumeHeavyAfterDeployment()
								return fault.Denied
							}
							a.upgradeRelease = func() { leave(); a.resumeHeavyAfterDeployment() }
						}
					}
				}
			}
			message := ""
			if e != nil {
				message = "当前有维护、备份、恢复或执行任务，尚不能排空升级；请等待任务结束后重试"
			}
			writeJSON(w, 200, map[string]any{"passed": e == nil, "error": message, "version": Release, "frozen": a.maintenance.Load()})
			return nil
		})
	}
}

func (a *App) upgradeApplicationIdle(ctx context.Context) error {
	if a.maintenance.Load() || a.resourceDrained.Load() {
		return errors.New("maintenance active")
	}
	var busy bool
	err := a.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE state='running')
 OR EXISTS(SELECT 1 FROM agent_installations WHERE state='running')
 OR EXISTS(SELECT 1 FROM ai_turns WHERE state='running')
 OR EXISTS(SELECT 1 FROM database_work_orders WHERE state='running')`).Scan(&busy)
	if err != nil || busy {
		return errors.New("active task or unavailable inventory")
	}
	return nil
}
