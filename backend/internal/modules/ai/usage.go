package ai

import (
	"context"
	"encoding/json"
	"errors"
	"next-terminal/backend/internal/platform/secretbox"
)

var ErrDailyBudget = errors.New("ai_daily_budget_exceeded")
var ErrTurnBudget = errors.New("ai_turn_budget_exceeded")
var ErrPricing = errors.New("ai_pricing_required")

func (s *Service) openProfile(p Profile, encrypted []byte) (Profile, error) {
	if len(encrypted) > 0 {
		aad := "ai-profile:" + p.ID
		if p.secretReference != "" {
			aad = "settings:llm-api-key:" + p.secretReference
		}
		plain, e := secretbox.Open(s.key, encrypted, aad)
		if e != nil {
			return p, e
		}
		p.APIKey = string(plain)
		clear(plain)
	}
	return p, nil
}
func cost(tokens int, rate int64) int64 { return (int64(tokens)*rate + 999) / 1000 }
func (s *Service) ReserveUsage(ctx context.Context, user, turn string, p Profile, messages []Message, policy Policy) error {
	// UTF-8 byte count plus envelope allowance is deliberately conservative. Rates
	// are administrator-supplied USD micros / 1000 tokens, not vendor billing data.
	raw, _ := json.Marshal(messages)
	inputBound := len(raw) + 65536
	reserved := cost(inputBound, p.InputRate) + cost(p.MaxOutputTokens, p.OutputRate)
	if (policy.DailyBudgetMicros > 0 || policy.MaxTurnCostMicros > 0) && (p.InputRate == 0 || p.OutputRate == 0) {
		return ErrPricing
	}
	if policy.MaxTurnCostMicros > 0 && reserved > policy.MaxTurnCostMicros {
		return ErrTurnBudget
	}
	var used int64
	if e := s.db.Row(ctx, `SELECT coalesce(sum(charged_micros),0) FROM ai_usage_ledger WHERE user_id=$1 AND created_at>=date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, user).Scan(&used); e != nil {
		return e
	}
	if policy.DailyBudgetMicros > 0 && used+reserved > policy.DailyBudgetMicros {
		return ErrDailyBudget
	}
	_, e := s.db.Exec(ctx, `INSERT INTO ai_usage_ledger(turn_id,user_id,profile_id,model,reserved_micros,charged_micros,input_rate,output_rate) VALUES($1,$2,$3,$4,$5,$5,$6,$7)`, turn, user, p.ID, p.Model, reserved, p.InputRate, p.OutputRate)
	return e
}
func (s *Service) SettleUsage(ctx context.Context, turn string, r Reply) error {
	_, e := s.db.Exec(ctx, `UPDATE ai_usage_ledger SET prompt_tokens=$2::bigint,completion_tokens=$3::bigint,usage_known=$4,charged_micros=CASE WHEN $4 THEN (($2::bigint*input_rate+999)/1000+($3::bigint*output_rate+999)/1000) ELSE charged_micros END WHERE turn_id=$1`, turn, r.PromptTokens, r.CompletionTokens, r.UsageKnown)
	return e
}

type UsageRow struct {
	UserID       string `json:"userId"`
	Model        string `json:"model"`
	Day          string `json:"day"`
	Requests     int    `json:"requests"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	CostMicros   int64  `json:"costMicros"`
	Unknown      int    `json:"unknownUsage"`
	Unpriced     int    `json:"unpricedRequests"`
}

func (s *Service) Usage(ctx context.Context, user string, days int) ([]UsageRow, error) {
	rows, e := s.db.Rows(ctx, `SELECT user_id::text,model,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD'),count(*),sum(prompt_tokens),sum(completion_tokens),sum(charged_micros),count(*) FILTER(WHERE NOT usage_known),count(*) FILTER(WHERE input_rate=0 OR output_rate=0) FROM ai_usage_ledger WHERE ($1='' OR user_id::text=$1) AND created_at>now()-($2::int*interval '1 day') GROUP BY 1,2,3 ORDER BY 3 DESC,1,2 LIMIT 1000`, user, days)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []UsageRow{}
	for rows.Next() {
		var v UsageRow
		if e = rows.Scan(&v.UserID, &v.Model, &v.Day, &v.Requests, &v.InputTokens, &v.OutputTokens, &v.CostMicros, &v.Unknown, &v.Unpriced); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
