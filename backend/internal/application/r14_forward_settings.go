package application

import (
	"context"
	"encoding/json"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
)

type nativeForwardRule = system.NativeForwardRule
type nativeForwardSettings struct {
	Enabled bool                `json:"enabled"`
	Version int64               `json:"version"`
	Rules   []nativeForwardRule `json:"rules"`
}

func (a *App) forwardSettings(ctx context.Context) (v nativeForwardSettings, e error) {
	var raw []byte
	e = a.db.Row(ctx, `SELECT enabled,version,rules FROM native_forward_settings WHERE singleton=1`).Scan(&v.Enabled, &v.Version, &raw)
	if e == nil {
		e = json.Unmarshal(raw, &v.Rules)
	}
	return
}
func forwardAddress(ip string) bool { return system.ForwardAddress(ip) }
func (a *App) nativeForwardConfig(w http.ResponseWriter, r *http.Request) error {
	u, e := a.require(r, "identity.manage")
	if e != nil {
		return e
	}
	if u.Type != "admin" {
		return fault.Denied
	}
	if r.Method == "GET" {
		var v nativeForwardSettings
		var revision int64
		var desired, pending bool
		e = a.db.Within(r.Context(), func(c context.Context) error {
			if e := a.db.Row(c, `SELECT revision FROM configuration_head WHERE singleton=1 FOR SHARE`).Scan(&revision); e != nil {
				return e
			}
			var err error
			v, err = a.forwardSettings(c)
			if err != nil {
				return err
			}
			return a.db.Row(c, `SELECT (r.snapshot->>'ssh-server-port-forwarding-enabled')::boolean,c.pending FROM configuration_revisions r JOIN configuration_security c ON c.revision=r.id WHERE r.id=$1`, revision).Scan(&desired, &pending)
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, struct {
			nativeForwardSettings
			DesiredEnabled        bool  `json:"desiredEnabled"`
			ConfigurationRevision int64 `json:"configurationRevision"`
			Pending               bool  `json:"pending"`
		}{v, desired, revision, pending})
		return nil
	}
	var in struct {
		backupProof
		nativeForwardSettings
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if len(in.Rules) > 100 || (in.Enabled && len(in.Rules) == 0) {
		return fault.Invalid
	}
	// Both editors write the same immutable configuration revision. Lock order is
	// configuration head, then domain row, matching the central settings writer.
	e = a.withPasswordProof(r, in.backupProof, "identity.manage", func(ctx context.Context, u *identity.User) error {
		var revision, version int64
		if e := a.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR UPDATE`).Scan(&revision); e != nil {
			return e
		}
		if e := a.db.Row(ctx, `SELECT version FROM native_forward_settings WHERE singleton=1`).Scan(&version); e != nil {
			return e
		}
		if version != in.Version {
			return fault.Conflict
		}
		raw, _ := json.Marshal(system.EncodeForwardRules(in.Rules))
		enabled, _ := json.Marshal(in.Enabled)
		v, e := a.system.SaveConfiguration(system.WithForwardConfigurationProof(ctx), &revision, map[string]json.RawMessage{system.ForwardEnabledKey: enabled, system.ForwardRulesKey: raw}, u.ID)
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "native.forward_settings_saved", requestID(r), sourceIP(r), map[string]string{"enabled": strconv.FormatBool(in.Enabled), "rules": strconv.Itoa(len(in.Rules)), "revision": strconv.FormatInt(v.ID, 10)})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
