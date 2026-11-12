package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"next-terminal/backend/internal/modules/ai"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"time"
	"unicode/utf8"
)

// This adapter calls the existing session/file use cases. AI never creates its
// own SSH connection, bypasses channel policy, or forwards tool output to a model.
type aiLocalResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
	err    error
}

func (v *aiLocalResponse) Header() http.Header  { return v.header }
func (v *aiLocalResponse) WriteHeader(code int) { v.status = code }
func (v *aiLocalResponse) Write(b []byte) (int, error) {
	if v.body.Len()+len(b) > 262144 {
		v.err = fault.Invalid
		return 0, fault.Invalid
	}
	return v.body.Write(b)
}
func (a *App) aiUseCase(r *http.Request, path, method string, payload any, session, task string, fn func(http.ResponseWriter, *http.Request) error) (string, error) {
	var raw []byte
	switch v := payload.(type) {
	case []byte:
		raw = v
	case nil:
	default:
		raw, _ = json.Marshal(v)
	}
	req := r.Clone(r.Context())
	req.URL, _ = url.Parse(path)
	req.Method = method
	req.Body = io.NopCloser(bytes.NewReader(raw))
	req.ContentLength = int64(len(raw))
	req.Header = req.Header.Clone()
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", session)
	req.SetPathValue("task", task)
	out := &aiLocalResponse{header: http.Header{}}
	e := fn(out, req)
	if e == nil {
		e = out.err
	}
	if e != nil {
		return "", e
	}
	return out.body.String(), nil
}
func (a *App) aiToolScope(r *http.Request, c ai.Conversation, name string) (asset resource.Asset, av, cv, policy, access int64, err error) {
	u, e := a.authenticated(r)
	if e != nil {
		err = e
		return
	}
	if err = a.aiService().RequireExecution(r.Context()); err != nil {
		return
	}
	if _, err = a.aiReadContext(r.Context(), u, c); err != nil {
		return
	}
	cfg, e := a.aiService().Settings(r.Context())
	if e != nil {
		err = e
		return
	}
	if !cfg.Enabled || !cfg.ReadonlyEnabled || c.ContextType == "" {
		err = fault.Denied
		return
	}
	policy = cfg.Version
	aid := c.ContextID
	if c.ContextType == "session" {
		v, e := a.sessions.Get(r.Context(), c.ContextID)
		if e != nil {
			err = e
			return
		}
		aid = v.AssetID
		if name != "asset_inspect" && name != "audit_commands" {
			if !cfg.ShellEnabled || v.ActorID != u.ID || v.Protocol != "ssh" || v.State != "connected" || (v.Readonly && (name == "session_command" || name == "file_write")) {
				err = fault.Denied
				return
			}
			if _, err = a.checkLive(r.Context(), v, token(r)); err != nil {
				return
			}
		}
	}
	if name != "asset_inspect" && c.ContextType != "session" {
		err = fault.Denied
		return
	}
	if name == "audit_commands" && !cfg.AuditEnabled {
		err = fault.Denied
		return
	}
	switch name {
	case "asset_inspect", "audit_commands", "session_command", "file_read", "file_write":
	default:
		err = fault.Invalid
		return
	}
	asset, err = a.sessionAsset(r.Context(), u, aid)
	if err != nil {
		return
	}
	av = asset.Version
	cred, e := a.vault.Get(r.Context(), asset.CredentialID)
	if e != nil {
		err = e
		return
	}
	cv = cred.ActiveVersion
	gate, e := a.aiService().AssetAccess(r.Context(), aid)
	if e != nil {
		err = e
		return
	}
	access = gate.Version
	return
}
func (a *App) aiToolList(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return fault.Invalid
	}
	c, e := a.aiService().Conversation(r.Context(), u.ID, id)
	if e != nil {
		return e
	}
	if _, e = a.aiReadContext(r.Context(), u, c); e != nil {
		return e
	}
	v, e := a.aiService().Executions(r.Context(), u.ID, id)
	if e != nil {
		return e
	}
	writeJSON(w, 200, v)
	return nil
}
func (a *App) aiToolPropose(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return fault.Invalid
	}
	var in struct {
		ClientID  string          `json:"clientId"`
		TurnID    string          `json:"turnId"`
		CallID    string          `json:"callId"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	if !resource.ValidID(in.ClientID) || (in.TurnID != "" && !resource.ValidID(in.TurnID)) {
		return fault.Invalid
	}
	var args struct {
		Command string `json:"command"`
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	d := json.NewDecoder(bytes.NewReader(in.Arguments))
	d.DisallowUnknownFields()
	if d.Decode(&args) != nil {
		return fault.Invalid
	}
	if (in.Name == "asset_inspect" || in.Name == "audit_commands") && (args.Command != "" || args.Path != "" || args.Content != "") {
		return fault.Invalid
	}
	if in.Name == "session_command" && (args.Path != "" || args.Content != "") {
		return fault.Invalid
	}
	if (in.Name == "file_read" || in.Name == "file_write") && args.Command != "" {
		return fault.Invalid
	}
	if in.Name == "file_read" && args.Content != "" {
		return fault.Invalid
	}

	c, e := a.aiService().Conversation(r.Context(), u.ID, id)
	if e != nil {
		return e
	}
	if in.TurnID != "" {
		ts, e := a.aiService().Turns(r.Context(), u.ID, id)
		if e != nil {
			return e
		}
		found := false
		for _, t := range ts {
			if t.ID == in.TurnID && t.State == "succeeded" {
				for _, call := range t.ToolCalls {
					if call.ID == in.CallID && call.Name == in.Name {
						var expected any
						var actual any
						_ = json.Unmarshal([]byte(call.Arguments), &expected)
						_ = json.Unmarshal(in.Arguments, &actual)
						x, _ := json.Marshal(expected)
						y, _ := json.Marshal(actual)
						found = bytes.Equal(x, y)
					}
				}
			}
		}
		if !found {
			return fault.Invalid
		}
	}
	_, av, cv, policy, access, e := a.aiToolScope(r, c, in.Name)
	if e != nil {
		return e
	}
	v := ai.Execution{ConversationID: id, TurnID: in.TurnID, UserID: u.ID, ClientID: in.ClientID, Name: in.Name, AssetVersion: av, CredentialVersion: cv, Arguments: ai.ToolArguments{PolicyVersion: policy, AccessVersion: access}}
	if c.ContextType == "session" {
		v.Arguments.SessionID = c.ContextID
	}
	switch in.Name {
	case "session_command":
		if strings.TrimSpace(args.Command) == "" || len(args.Command) > 4096 || strings.ContainsRune(args.Command, 0) {
			return fault.Invalid
		}
		v.Arguments.Command = args.Command
	case "file_read", "file_write":
		if args.Path == "" || len(args.Path) > 4096 || strings.ContainsRune(args.Path, 0) {
			return fault.Invalid
		}
		v.Arguments.Path = args.Path
		if in.Name == "file_write" {
			if len(args.Content) > 8192 || !utf8.ValidString(args.Content) || strings.ContainsRune(args.Content, 0) {
				return fault.Invalid
			}
			raw, e := a.aiUseCase(r, "/api/sessions/"+c.ContextID+"/files/read?text=true&path="+url.QueryEscape(args.Path), "GET", nil, c.ContextID, "", a.fileRead)
			if e != nil {
				return e
			}
			var original struct {
				Content string `json:"content"`
				Hash    string `json:"sha256"`
			}
			if json.Unmarshal([]byte(raw), &original) != nil || len(original.Content) > 16384 {
				return fault.Invalid
			}
			v.Arguments.Content = args.Content
			v.Arguments.ExpectedHash = original.Hash
			v.Arguments.Before = original.Content
		}
	}
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		_, av2, cv2, p2, a2, err := a.aiToolScope(r.Clone(ctx), c, in.Name)
		if err != nil {
			return err
		}
		if av != av2 || cv != cv2 || policy != p2 || access != a2 {
			return fault.Conflict
		}
		v, err = a.aiService().Propose(ctx, v)
		if err != nil {
			return err
		}
		return a.audit.Detail(ctx, u.ID, "ai.tool_proposed", requestID(r), sourceIP(r), map[string]string{"executionId": v.ID, "parameterHash": v.ParameterHash, "tool": v.Name})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, v)
	return nil
}
func (a *App) aiToolAction(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	id := r.PathValue("execution")
	if !resource.ValidID(id) {
		return fault.Invalid
	}
	s := a.aiService()
	v, e := s.Execution(r.Context(), u.ID, id)
	if e != nil {
		return e
	}
	action := r.PathValue("action")
	if action == "cancel" {
		if e = s.CancelExecution(r.Context(), u.ID, id); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}
	check := func(ctx context.Context) error {
		c, e := s.Conversation(ctx, u.ID, v.ConversationID)
		if e != nil {
			return e
		}
		_, av, cv, p, ac, e := a.aiToolScope(r.Clone(ctx), c, v.Name)
		if e != nil {
			return e
		}
		if av != v.AssetVersion || cv != v.CredentialVersion || p != v.Arguments.PolicyVersion || ac != v.Arguments.AccessVersion || ai.ToolHash(v) != v.ParameterHash {
			return fault.Conflict
		}
		return nil
	}
	if action == "decide" {
		var in struct {
			Decision      string `json:"decision"`
			ParameterHash string `json:"parameterHash"`
			Revision      int64  `json:"revision"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			if in.Decision == "approve" {
				if e := check(ctx); e != nil {
					return e
				}
			}
			if e := s.Decide(ctx, u.ID, id, in.ParameterHash, in.Decision, in.Revision); e != nil {
				return e
			}
			return a.audit.Detail(ctx, u.ID, "ai.tool_"+in.Decision, requestID(r), sourceIP(r), map[string]string{"executionId": id, "parameterHash": in.ParameterHash})
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}
	if action != "execute" {
		return fault.Invalid
	}
	if e = check(r.Context()); e != nil {
		_ = s.FinishExecution(r.Context(), u.ID, id, "needs_revision", "", "authorization_or_parameters_changed")
		return e
	}
	if e = s.ClaimExecution(r.Context(), u.ID, id); e != nil {
		return e
	}
	// Detached delivery, bounded execution. Refresh cannot replay the claimed row.
	duration := 10 * time.Second
	cfg, e := s.Settings(r.Context())
	if e == nil && cfg.CommandTimeoutSeconds > 0 {
		duration = time.Duration(cfg.CommandTimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), duration)
	defer cancel()
	stop := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				current, e := s.Execution(ctx, u.ID, id)
				if e != nil || current.CancelRequested || a.maintenance.Load() || check(ctx) != nil {
					cancel()
					return
				}
			}
		}
	}()
	var output string
	if e = check(ctx); e == nil {
		output, e = a.aiExecuteAdapter(r.Clone(ctx), v)
	}
	close(stop)
	<-watchDone
	state, reason := "succeeded", ""
	if e != nil {
		state = "failed"
		reason = "tool_failed"
		if errors.Is(e, fault.Conflict) {
			state = "needs_revision"
			reason = "target_changed"
		}
	}
	if ctx.Err() != nil {
		state = "unknown"
		reason = "unknown_remote_outcome"
	}
	if v.Name == "session_command" && e == nil {
		var command struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal([]byte(output), &command)
		if command.State != "succeeded" {
			state = "failed"
			reason = "command_failed"
			if command.State == "timeout" || command.State == "interrupted" {
				state = "unknown"
				reason = "unknown_remote_outcome"
			}
		}
	}
	c, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if e = check(c); e != nil {
		output = ""
		if state == "succeeded" {
			state = "unknown"
			reason = "authorization_changed_after_execution"
		}
	}
	e = a.db.Within(c, func(c context.Context) error {
		if err := s.FinishExecution(c, u.ID, id, state, output, reason); err != nil {
			return err
		}
		return a.audit.Detail(c, u.ID, "ai.tool_finished", requestID(r), sourceIP(r), map[string]string{"executionId": id, "state": state, "reason": reason})
	})
	if e != nil {
		return e
	}
	result, e := s.Execution(c, u.ID, id)
	if e != nil {
		return e
	}
	writeJSON(w, 200, result)
	return nil
}
func (a *App) aiExecuteAdapter(r *http.Request, v ai.Execution) (string, error) {
	session := v.Arguments.SessionID
	switch v.Name {
	case "asset_inspect":
		u, e := a.authenticated(r)
		if e != nil {
			return "", e
		}
		c, e := a.aiService().Conversation(r.Context(), u.ID, v.ConversationID)
		if e != nil {
			return "", e
		}
		return a.aiReadContext(r.Context(), u, c)
	case "audit_commands":
		rows, total, e := a.audit.CommandPage(r.Context(), session, 0, 100)
		if e != nil {
			return "", e
		}
		b, e := json.Marshal(map[string]any{"items": rows, "total": total})
		if len(b) > 262144 {
			return "", fault.Invalid
		}
		return string(b), e
	case "session_command":
		return a.aiUseCase(r, "/api/sessions/"+session+"/commands", "POST", map[string]string{"command": v.Arguments.Command}, session, "", a.executeCommand)
	case "file_read":
		return a.aiUseCase(r, "/api/sessions/"+session+"/files/read?text=true&path="+url.QueryEscape(v.Arguments.Path), "GET", nil, session, "", a.fileRead)
	case "file_write":
		base := "/api/sessions/" + session + "/uploads"
		_, e := a.aiUseCase(r, base, "POST", map[string]any{"id": v.ID, "path": v.Arguments.Path, "size": len([]byte(v.Arguments.Content)), "expectedHash": v.Arguments.ExpectedHash}, session, "", a.uploadCreate)
		if e != nil {
			return "", e
		}
		if len(v.Arguments.Content) > 0 {
			if _, e = a.aiUseCase(r, base+"/"+v.ID+"?offset=0", "PUT", []byte(v.Arguments.Content), session, v.ID, a.uploadChunk); e != nil {
				_ = a.files.Cancel(context.WithoutCancel(r.Context()), v.ID, v.UserID)
				return "", e
			}
		}
		return a.aiUseCase(r, base+"/"+v.ID+"/commit", "POST", nil, session, v.ID, a.uploadCommit)
	}
	return "", fault.Invalid
}
