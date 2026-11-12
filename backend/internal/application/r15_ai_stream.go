package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"next-terminal/backend/internal/modules/ai"
	"next-terminal/backend/internal/modules/resource"
	"strconv"
	"time"
)

func (a *App) aiCancel(w http.ResponseWriter, r *http.Request) error {
	u, err := a.authenticated(r)
	if err != nil {
		return err
	}
	id, turn := r.PathValue("id"), r.PathValue("turn")
	if !resource.ValidID(id) || !resource.ValidID(turn) {
		return problem(422, "invalid_id", "请求参数无效")
	}
	err = a.db.Within(r.Context(), func(ctx context.Context) error {
		if _, e := a.authenticated(r.Clone(ctx)); e != nil {
			return e
		}
		if e := a.aiService().Cancel(ctx, u.ID, id, turn); e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "ai.turn_cancel_requested", requestID(r), sourceIP(r), map[string]string{"conversationId": id, "turnId": turn})
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

func (a *App) aiMessageStream(w http.ResponseWriter, r *http.Request) error {
	u, err := a.authenticated(r)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	var in struct {
		Text            string `json:"text"`
		ClientMessageID string `json:"clientMessageId"`
		ContextDigest   string `json:"contextDigest"`
	}
	if !resource.ValidID(id) {
		return problem(422, "invalid_id", "请求参数无效")
	}
	if err = decode(w, r, &in); err != nil {
		return err
	}
	if !resource.ValidID(in.ClientMessageID) {
		return problem(422, "invalid_id", "请求参数无效")
	}
	select {
	case a.aiSlots <- struct{}{}:
		defer func() { <-a.aiSlots }()
	default:
		return problem(429, "ai_busy", "AI 请求较多，请稍后重试")
	}
	s := a.aiService()
	var p ai.Prepared
	err = a.db.Within(r.Context(), func(ctx context.Context) error {
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
		return a.audit.Detail(ctx, u.ID, "ai.turn_started", requestID(r), sourceIP(r), map[string]string{"conversationId": id, "turnId": p.Turn.ID, "profileId": p.Profile.ID, "mode": "stream"})
	})
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	connected := true
	emit := func(event string, v any) {
		if !connected {
			return
		}
		_ = controller.SetWriteDeadline(time.Now().Add(2 * time.Second))
		b, _ := json.Marshal(v)
		if _, e := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); e != nil {
			connected = false
			return
		}
		if e := controller.Flush(); e != nil {
			connected = false
		}
	}
	emit("turn", p.Turn)
	if !p.Fresh {
		emit("done", p.Turn)
		return nil
	}
	return a.runAITurn(r, p, u.ID, emit)
}

func (a *App) runAITurn(r *http.Request, p ai.Prepared, userID string, emit func(string, any)) error {
	s := a.aiService()
	id := p.Conversation.ID
	var err error
	// The handler remains in the maintenance request gate until durable finalization.
	// Browser disconnect only stops delivery. It never starts a second provider call.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 60*time.Second)
	defer cancel()
	a.backgroundMu.Lock()
	life := a.backgroundParent
	a.backgroundMu.Unlock()
	type outcome struct {
		reply ai.Reply
		err   error
	}
	progress := make(chan ai.Reply, 1)
	done := make(chan outcome, 1)
	go func() {
		reply, e := s.GenerateStream(ctx, p.Profile, p.Messages, func(v ai.Reply) error {
			select {
			case progress <- v:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		done <- outcome{reply, e}
	}()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	latest := ai.Reply{}
	saved := ""
	reason := ""
	var outcomeResult outcome
	check := func(checkCtx context.Context) error {
		current, e := a.authenticated(r.Clone(checkCtx))
		if e != nil {
			return e
		}
		if _, e = a.aiReadContext(checkCtx, current, p.Conversation); e != nil {
			return e
		}
		return s.StillEnabled(checkCtx, p.Profile)
	}
loop:
	for {
		select {
		case v := <-progress:
			latest = v
		case v := <-done:
			outcomeResult = v
			break loop
		case <-ticker.C:
			if reason != "" {
				continue
			}
			if a.maintenance.Load() || (life != nil && life.Err() != nil) || ctx.Err() != nil {
				reason = "request_interrupted"
				cancel()
				continue
			}
			checkCtx, checkCancel := context.WithTimeout(ctx, 2*time.Second)
			if e := check(checkCtx); e != nil {
				reason = "authorization_changed"
			} else if stopped, e := s.CancelRequested(checkCtx, userID, p.Turn.ID); e != nil {
				reason = "request_interrupted"
			} else if stopped {
				reason = "user_cancelled"
			}
			if reason == "" && latest.Text != saved {
				var e error
				p.Turn, e = s.Progress(checkCtx, userID, p.Turn, latest)
				if e != nil {
					reason = "request_interrupted"
				} else {
					saved = latest.Text
					emit("turn", p.Turn)
				}
			}
			checkCancel()
			if reason != "" {
				cancel()
			}
		}
	}
	p.Profile.APIKey = ""
	if outcomeResult.err != nil && reason == "" {
		reason = "model_request_failed"
	}
	if errors.Is(outcomeResult.err, ai.ErrExecutionUnavailable) {
		reason = "authorization_changed"
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	var result ai.Turn
	err = a.db.Within(finishCtx, func(c context.Context) error {
		if e := check(c); e != nil {
			reason = "authorization_changed"
		}
		var e error
		result, e = s.Finish(c, userID, id, p.Turn, outcomeResult.reply, reason)
		if e != nil {
			return e
		}
		if e = s.CompleteJob(c, p.Turn.ID); e != nil {
			return e
		}
		return a.audit.Detail(c, userID, "ai.turn_finished", requestID(r), sourceIP(r), map[string]string{"conversationId": id, "turnId": result.ID, "state": result.State, "reason": result.Reason, "promptTokens": strconv.Itoa(result.PromptTokens), "completionTokens": strconv.Itoa(result.CompletionTokens)})
	})
	// Headers were committed: never append an HTTP JSON error to the event stream.
	if err != nil {
		emit("error", map[string]string{"message": "状态保存失败，请从历史对话检查结果；请勿重复提交。"})
		return nil
	}
	emit("done", result)
	return nil
}
