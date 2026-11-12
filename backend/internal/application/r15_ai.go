package application

import (
	"context"
	"errors"
	"net/http"
	"next-terminal/backend/internal/modules/ai"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"time"
)

func (a *App) aiService() *ai.Service { return ai.New(a.db, a.config.VaultKey) }
func (a *App) registerAI(handle func(string, func(http.ResponseWriter, *http.Request) error)) {
	handle("POST /api/ai/conversations/{id}/messages/queued", a.aiQueue)
	handle("GET /api/ai/ws", a.aiSocket)
	handle("POST /api/ai/conversations/{id}/history/{action}", a.aiHistoryAction)
	handle("GET /api/ai/usage", a.aiUsage)
	handle("GET /api/ai/preferences", a.aiPreferences)
	handle("PUT /api/ai/preferences", a.aiPreferences)
	handle("GET /api/admin/ai/profiles/{id}/models", a.aiModels)
	handle("GET /api/ai/conversations/{id}/tools", a.aiToolList)
	handle("POST /api/ai/conversations/{id}/tools", a.aiToolPropose)
	handle("POST /api/ai/tools/{execution}/{action}", a.aiToolAction)
	handle("GET /api/ai/status", a.aiSettings)
	handle("GET /api/admin/ai/settings", a.aiSettings)
	handle("PUT /api/admin/ai/settings", a.aiSettings)
	handle("GET /api/admin/ai/profiles", a.aiProfiles)
	handle("POST /api/admin/ai/profiles", a.aiProfiles)
	handle("PUT /api/admin/ai/profiles/{id}", a.aiProfiles)
	handle("DELETE /api/admin/ai/profiles/{id}", a.aiProfiles)
	handle("GET /api/ai/profiles", a.aiProfiles)
	handle("GET /api/ai/conversations", a.aiConversations)
	handle("POST /api/ai/conversations", a.aiConversations)
	handle("GET /api/ai/conversations/{id}", a.aiConversation)
	handle("DELETE /api/ai/conversations/{id}", a.aiConversation)
	handle("POST /api/ai/conversations/{id}/messages", a.aiMessage)
	handle("POST /api/ai/conversations/{id}/messages/stream", a.aiMessageStream)
	handle("POST /api/ai/conversations/{id}/turns/{turn}/cancel", a.aiCancel)
	handle("GET /api/ai/context", a.aiContextPreview)
	handle("GET /api/admin/ai/assets/{id}", a.aiAssetAccess)
	handle("PUT /api/admin/ai/assets/{id}", a.aiAssetAccess)
}
func (a *App) aiSettings(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	if r.URL.Path == "/api/admin/ai/settings" {
		if u.Type != "admin" {
			return fault.Denied
		}
		if e := a.allowed(r.Context(), u, "identity.manage"); e != nil {
			return e
		}
	}
	s := a.aiService()
	if r.Method == "GET" {
		v, e := s.Settings(r.Context())
		if e != nil {
			return e
		}
		if r.URL.Path == "/api/ai/status" {
			writeJSON(w, 200, map[string]any{"enabled": v.Enabled, "readonlyEnabled": v.ReadonlyEnabled, "shellEnabled": v.ShellEnabled, "auditEnabled": v.AuditEnabled, "version": v.Version})
		} else {
			writeJSON(w, 200, v)
		}
		return nil
	}
	var v ai.Settings
	if e := decode(w, r, &v); e != nil {
		return e
	}
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		u, e := a.require(r.Clone(ctx), "identity.manage")
		if e != nil {
			return e
		}
		if u.Type != "admin" {
			return fault.Denied
		}
		if e = s.SaveSettings(ctx, v, u.ID); e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "ai.settings_changed", requestID(r), sourceIP(r), map[string]string{"enabled": strconv.FormatBool(v.Enabled), "readonlyEnabled": strconv.FormatBool(v.ReadonlyEnabled)})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *App) aiProfiles(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	admin := r.URL.Path != "/api/ai/profiles"
	if admin {
		if u.Type != "admin" {
			return fault.Denied
		}
		if e = a.allowed(r.Context(), u, "identity.manage"); e != nil {
			return e
		}
	}
	s := a.aiService()
	if r.Method == "GET" {
		v, e := s.Profiles(r.Context(), admin)
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	}
	id := r.PathValue("id")
	if r.Method != "POST" && !resource.ValidID(id) {
		return fault.Invalid
	}
	var v ai.Profile
	if r.Method != "DELETE" {
		if e = decode(w, r, &v); e != nil {
			return e
		}
		v.ID = id
		if r.Method == "POST" && v.Version != 0 {
			return fault.Invalid
		}
		if r.Method == "PUT" && v.Version < 1 {
			return fault.Invalid
		}
	}
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		current, e := a.require(r.Clone(ctx), "identity.manage")
		if e != nil {
			return e
		}
		if current.Type != "admin" {
			return fault.Denied
		}
		action := "ai.profile_saved"
		if r.Method == "DELETE" {
			e = s.DeleteProfile(ctx, id)
			action = "ai.profile_deleted"
		} else {
			id, e = s.SaveProfile(ctx, v)
		}
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, current.ID, action, requestID(r), sourceIP(r), map[string]string{"profileId": id})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]string{"id": id})
	return nil
}
func (a *App) aiConversations(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	s := a.aiService()
	if r.Method == "GET" {
		v, e := s.Conversations(r.Context(), u.ID)
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	}
	var v struct {
		ProfileID   string `json:"profileId"`
		Title       string `json:"title"`
		ContextType string `json:"contextType"`
		ContextID   string `json:"contextId"`
	}
	if e = decode(w, r, &v); e != nil {
		return e
	}
	if !resource.ValidID(v.ProfileID) {
		return fault.Invalid
	}
	var id string
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		if _, e := a.authenticated(r.Clone(ctx)); e != nil {
			return e
		}
		var e error
		scope := ai.Conversation{ContextType: v.ContextType, ContextID: v.ContextID}
		if _, e = a.aiReadContext(ctx, u, scope); e != nil {
			return e
		}
		id, e = s.Create(ctx, u.ID, v.ProfileID, v.Title, scope)
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "ai.conversation_created", requestID(r), sourceIP(r), map[string]string{"conversationId": id})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]string{"id": id})
	return nil
}
func (a *App) aiConversation(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return fault.Invalid
	}
	s := a.aiService()
	if r.Method == "GET" {
		v, e := s.Conversation(r.Context(), u.ID, id)
		if e != nil {
			return e
		}
		if _, e = a.aiReadContext(r.Context(), u, v); e != nil {
			return e
		}
		turns, e := s.Turns(r.Context(), u.ID, id)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"conversation": v, "turns": turns})
		return nil
	}
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		if _, e := a.authenticated(r.Clone(ctx)); e != nil {
			return e
		}
		if e := s.Delete(ctx, u.ID, id); e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "ai.conversation_deleted", requestID(r), sourceIP(r), map[string]string{"conversationId": id})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *App) aiMessage(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return fault.Invalid
	}
	var in struct {
		Text            string `json:"text"`
		ClientMessageID string `json:"clientMessageId"`
		ContextDigest   string `json:"contextDigest"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	if !resource.ValidID(in.ClientMessageID) {
		return fault.Invalid
	}
	select {
	case a.aiSlots <- struct{}{}:
		defer func() { <-a.aiSlots }()
	default:
		return problem(429, "ai_busy", "AI 请求较多，请稍后重试")
	}
	s := a.aiService()
	var p ai.Prepared
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		if _, e := a.authenticated(r.Clone(ctx)); e != nil {
			return e
		}
		var e error
		p, e = s.Begin(ctx, u.ID, id, in.ClientMessageID, in.Text)
		if e != nil {
			return e
		}
		if e = a.aiAttachContext(ctx, r, u, &p, in.ContextDigest); e != nil {
			return e
		}
		if !p.Fresh {
			return nil
		}
		return a.audit.Detail(ctx, u.ID, "ai.turn_started", requestID(r), sourceIP(r), map[string]string{"conversationId": id, "turnId": p.Turn.ID, "profileId": p.Profile.ID})
	})
	if e != nil {
		return e
	}
	if !p.Fresh {
		writeJSON(w, 200, p.Turn)
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	reply, generateErr := s.Generate(ctx, p.Profile, p.Messages)
	p.Profile.APIKey = ""
	reason := ""
	if generateErr != nil {
		reason = "model_request_failed"
	}
	if errors.Is(generateErr, ai.ErrExecutionUnavailable) {
		reason = "authorization_changed"
	}
	// Persist terminal status even when the browser disconnected. Never replay.
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	var result ai.Turn
	var authErr error
	e = a.db.Within(finishCtx, func(ctx context.Context) error {
		_, authErr = a.authenticated(r.Clone(ctx))
		if authErr == nil {
			authErr = s.StillEnabled(ctx, p.Profile)
		}
		if authErr == nil {
			current, e := a.authenticated(r.Clone(ctx))
			if e != nil {
				authErr = e
			} else {
				_, authErr = a.aiReadContext(ctx, current, p.Conversation)
			}
		}
		if authErr != nil {
			reason = "authorization_changed"
		}
		var err error
		result, err = s.Finish(ctx, u.ID, id, p.Turn, reply, reason)
		if err != nil {
			return err
		}
		return a.audit.Detail(ctx, u.ID, "ai.turn_finished", requestID(r), sourceIP(r), map[string]string{"conversationId": id, "turnId": result.ID, "state": result.State, "reason": result.Reason, "promptTokens": strconv.Itoa(result.PromptTokens), "completionTokens": strconv.Itoa(result.CompletionTokens)})
	})
	if e != nil {
		return e
	}
	if authErr != nil {
		return authErr
	}
	writeJSON(w, 200, result)
	return nil
}
