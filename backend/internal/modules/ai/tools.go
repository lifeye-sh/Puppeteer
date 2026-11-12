package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"next-terminal/backend/internal/platform/fault"
	"time"
)

type ToolArguments struct {
	Command       string `json:"command,omitempty"`
	Path          string `json:"path,omitempty"`
	Content       string `json:"content,omitempty"`
	SessionID     string `json:"sessionId,omitempty"`
	ExpectedHash  string `json:"expectedHash,omitempty"`
	Before        string `json:"before,omitempty"`
	PolicyVersion int64  `json:"policyVersion"`
	AccessVersion int64  `json:"accessVersion"`
}
type Execution struct {
	ID                string        `json:"id"`
	ConversationID    string        `json:"conversationId"`
	TurnID            string        `json:"turnId"`
	UserID            string        `json:"-"`
	ClientID          string        `json:"clientId"`
	Name              string        `json:"name"`
	Arguments         ToolArguments `json:"arguments"`
	ParameterHash     string        `json:"parameterHash"`
	AssetVersion      int64         `json:"assetVersion"`
	CredentialVersion int64         `json:"credentialVersion"`
	State             string        `json:"state"`
	Output            string        `json:"output"`
	Reason            string        `json:"reason"`
	CancelRequested   bool          `json:"cancelRequested"`
	Revision          int64         `json:"revision"`
	ExpiresAt         time.Time     `json:"expiresAt"`
}

func ToolHash(v Execution) string {
	raw, _ := json.Marshal(struct {
		Name                            string
		Arguments                       ToolArguments
		Conversation                    string
		AssetVersion, CredentialVersion int64
	}{v.Name, v.Arguments, v.ConversationID, v.AssetVersion, v.CredentialVersion})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

const executionColumns = `id,conversation_id,coalesce(turn_id::text,''),user_id,client_id,name,arguments,parameter_hash,asset_version,credential_version,state,output,reason,cancel_requested,revision,expires_at`

func (s *Service) Execution(ctx context.Context, user, id string) (v Execution, e error) {
	var raw []byte
	e = s.db.Row(ctx, `SELECT `+executionColumns+` FROM ai_tool_executions WHERE id=$1 AND user_id=$2`, id, user).Scan(&v.ID, &v.ConversationID, &v.TurnID, &v.UserID, &v.ClientID, &v.Name, &raw, &v.ParameterHash, &v.AssetVersion, &v.CredentialVersion, &v.State, &v.Output, &v.Reason, &v.CancelRequested, &v.Revision, &v.ExpiresAt)
	if e == nil {
		e = json.Unmarshal(raw, &v.Arguments)
	}
	return
}
func (s *Service) Executions(ctx context.Context, user, conversation string) ([]Execution, error) {
	if _, e := s.Conversation(ctx, user, conversation); e != nil {
		return nil, e
	}
	rows, e := s.db.Rows(ctx, `SELECT id FROM ai_tool_executions WHERE user_id=$1 AND conversation_id=$2 ORDER BY created_at DESC LIMIT 100`, user, conversation)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []Execution{}
	for _, id := range ids {
		v, e := s.Execution(ctx, user, id)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Service) Propose(ctx context.Context, v Execution) (Execution, error) {
	if e := s.RequireExecution(ctx); e != nil {
		return v, e
	}
	if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,151))`, v.UserID); e != nil {
		return v, e
	}
	var count int
	if e := s.db.Row(ctx, `SELECT count(*) FROM ai_tool_executions WHERE conversation_id=$1 AND client_id<>$2`, v.ConversationID, v.ClientID).Scan(&count); e != nil {
		return v, e
	}
	if count >= 500 {
		return v, fault.Conflict
	}
	v.ParameterHash = ToolHash(v)
	raw, _ := json.Marshal(v.Arguments)
	var id string
	e := s.db.Row(ctx, `INSERT INTO ai_tool_executions(conversation_id,turn_id,user_id,client_id,name,arguments,parameter_hash,asset_version,credential_version,state) VALUES($1,NULLIF($2,'')::uuid,$3,$4,$5,$6,$7,$8,$9,'awaiting_approval') ON CONFLICT(user_id,client_id) DO UPDATE SET client_id=EXCLUDED.client_id RETURNING id`, v.ConversationID, v.TurnID, v.UserID, v.ClientID, v.Name, raw, v.ParameterHash, v.AssetVersion, v.CredentialVersion).Scan(&id)
	if e != nil {
		return v, e
	}
	out, e := s.Execution(ctx, v.UserID, id)
	if e == nil && out.ParameterHash != v.ParameterHash {
		return v, fault.Conflict
	}
	return out, e
}
func (s *Service) Decide(ctx context.Context, user, id, hash, decision string, revision int64) error {
	if decision == "approve" {
		if e := s.RequireExecution(ctx); e != nil {
			return e
		}
	}
	if decision != "approve" && decision != "reject" {
		return fault.Invalid
	}
	state := "approved"
	if decision == "reject" {
		state = "rejected"
	}
	tag, e := s.db.Exec(ctx, `UPDATE ai_tool_executions SET state=$5,revision=revision+1 WHERE id=$1 AND user_id=$2 AND parameter_hash=$3 AND revision=$4 AND state='awaiting_approval' AND expires_at>now()`, id, user, hash, revision, state)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	_, e = s.db.Exec(ctx, `INSERT INTO ai_tool_approvals(execution_id,actor_id,parameter_hash,decision) VALUES($1,$2,$3,$4)`, id, user, hash, decision)
	return e
}
func (s *Service) ClaimExecution(ctx context.Context, user, id string) error {
	if e := s.RequireExecution(ctx); e != nil {
		return e
	}
	tag, e := s.db.Exec(ctx, `UPDATE ai_tool_executions x SET state='running',started_at=now(),revision=revision+1 WHERE x.id=$1 AND user_id=$2 AND state='approved' AND expires_at>now() AND NOT cancel_requested AND EXISTS(SELECT 1 FROM ai_tool_approvals a WHERE a.execution_id=x.id AND a.actor_id=$2 AND a.parameter_hash=x.parameter_hash AND a.decision='approve')`, id, user)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}
func (s *Service) FinishExecution(ctx context.Context, user, id, state, output, reason string) error {
	if len(output) > 262144 {
		return fault.Invalid
	}
	_, e := s.db.Exec(ctx, `UPDATE ai_tool_executions SET state=$3,output=$4,reason=$5,finished_at=now(),revision=revision+1 WHERE id=$1 AND user_id=$2 AND state IN ('approved','running')`, id, user, state, output, reason)
	return e
}
func (s *Service) CancelExecution(ctx context.Context, user, id string) error {
	tag, e := s.db.Exec(ctx, `UPDATE ai_tool_executions SET cancel_requested=true,state=CASE WHEN state='running' THEN state ELSE 'cancelled' END,revision=revision+1 WHERE id=$1 AND user_id=$2 AND state IN ('awaiting_approval','approved','running')`, id, user)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}
