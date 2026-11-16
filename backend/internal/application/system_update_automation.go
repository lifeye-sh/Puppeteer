package application

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"puppeteer/backend/internal/modules/identity"
	"puppeteer/backend/internal/platform/fault"
	"puppeteer/backend/internal/platform/systemupdate"
	"time"
)

// Delivery resumes after activation, through the existing durable notification
// queue. It is not required for host recovery, which remains independent.
func (a *App) collectUpdateNotifications(ctx context.Context) {
	if a.config.DeploySocket == "" {
		return
	}
	_, raw, e := a.updateCall(ctx, "GET", "/v1/updates/automation", nil)
	if e != nil {
		return
	}
	type notice struct {
		Task   string `json:"task"`
		Stage  string `json:"stage"`
		Actor  string `json:"actor"`
		Reason string `json:"reason"`
	}
	var state struct {
		Notices   []notice `json:"notices"`
		Actor     string   `json:"actor"`
		LastTask  string   `json:"lastTask"`
		LastStage string   `json:"lastStage"`
		Reason    string   `json:"reason"`
	}
	if json.Unmarshal(raw, &state) != nil || len(state.Notices) > 30 {
		return
	}
	// Older executors expose only their latest result. New executors retain
	// results across policy changes and tasks while the application is offline.
	if len(state.Notices) == 0 {
		state.Notices = append(state.Notices, notice{Task: state.LastTask, Stage: state.LastStage, Actor: state.Actor, Reason: state.Reason})
	}
	for _, n := range state.Notices {
		if decoded, e := hex.DecodeString(n.Task); e != nil || len(decoded) != 16 || n.Actor == "" {
			continue
		}
		event := ""
		label := ""
		switch n.Stage {
		case "prepared":
			event = "system.upgrade.prepared"
			label = "自动准备完成"
		case "succeeded":
			event = "system.upgrade.succeeded"
			label = "自动升级成功"
		case "failed", "rolled_back", "recovery_required":
			event = "system.upgrade.failed"
			label = "自动升级失败或已恢复原版本"
		default:
			continue
		}
		payload, _ := json.Marshal(map[string]string{"title": "Puppeteer 系统更新", "text": label + "。任务 " + n.Task + "：" + n.Reason + "。请登录系统更新页面查看详情。"})
		_, _ = a.db.Exec(ctx, `INSERT INTO notification_events(event_key,event_type,actor_id,payload) SELECT $1,$2,id,$4 FROM users WHERE id=$3 AND deleted_at IS NULL ON CONFLICT(event_key) DO NOTHING`, "upgrade:"+n.Task+":"+event, event, n.Actor, payload)
	}
}

func (a *App) registerUpdateAutomation(h func(string, func(http.ResponseWriter, *http.Request) error), admin func(*http.Request) error) {
	h("GET /api/admin/updates/history", func(w http.ResponseWriter, r *http.Request) error {
		if e := admin(r); e != nil {
			return e
		}
		code, body, e := a.updateCall(r.Context(), "GET", "/v1/updates/history", nil)
		if e != nil {
			return e
		}
		writeJSON(w, code, body)
		return nil
	})
	h("GET /api/admin/updates/automation", func(w http.ResponseWriter, r *http.Request) error {
		if e := admin(r); e != nil {
			return e
		}
		code, body, e := a.updateCall(r.Context(), "GET", "/v1/updates/automation", nil)
		if e != nil {
			return e
		}
		writeJSON(w, code, body)
		return nil
	})
	h("PUT /api/admin/updates/automation", func(w http.ResponseWriter, r *http.Request) error {
		if e := admin(r); e != nil {
			return e
		}
		var in struct {
			backupProof
			Policy        systemupdate.AutomationPolicy `json:"policy"`
			ConfirmedStop bool                          `json:"confirmedStop"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		if e := in.Policy.Validate(time.Now()); e != nil {
			return problem(422, "update_automation_invalid", e.Error())
		}
		if in.Policy.Mode == "window" && !in.ConfirmedStop {
			return problem(422, "update_confirmation_required", "请确认维护窗口内允许停止当前会话")
		}
		var envelope json.RawMessage
		if in.Policy.Mode != "off" {
			m, e := a.updates()
			if e != nil {
				return e
			}
			target, signed, e := m.SignedTarget(in.Policy.MaxVersion)
			if e != nil {
				return problem(422, "update_target_invalid", e.Error())
			}
			if target.Channel != in.Policy.Channel {
				return problem(422, "update_channel_invalid", "版本上限不属于授权渠道")
			}
			envelope = signed
		}
		actor := ""
		if e := a.withPasswordProof(r, in.backupProof, "identity.manage", func(ctx context.Context, u *identity.User) error {
			if u.Type != "admin" {
				return fault.Denied
			}
			actor = u.ID
			detail, _ := json.Marshal(in.Policy)
			return a.audit.Detail(ctx, u.ID, "system.upgrade_authorization_requested", requestID(r), sourceIP(r), map[string]string{"policy": string(detail)})
		}); e != nil {
			return e
		}
		code, body, e := a.updateCall(r.Context(), "POST", "/v1/updates/automation", map[string]any{"policy": in.Policy, "actor": actor, "confirmedStop": in.ConfirmedStop, "envelope": envelope})
		if e != nil {
			return e
		}
		writeJSON(w, code, body)
		return nil
	})
}
