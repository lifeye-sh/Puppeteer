package ai

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/platform/fault"
	"unicode/utf8"
)

type Policy struct {
	ShellEnabled          bool   `json:"shellEnabled"`
	AuditEnabled          bool   `json:"auditEnabled"`
	DailyBudgetMicros     int64  `json:"dailyBudgetMicros"`
	MaxTurnCostMicros     int64  `json:"maxTurnCostMicros"`
	CommandTimeoutSeconds int    `json:"commandTimeoutSeconds"`
	ShellPrompt           string `json:"shellPrompt"`
	AuditPrompt           string `json:"auditPrompt"`
}

func (v Policy) Valid() bool {
	return v.DailyBudgetMicros >= 0 && v.DailyBudgetMicros <= 100000000000 && v.MaxTurnCostMicros >= 0 && v.MaxTurnCostMicros <= 10000000000 && v.CommandTimeoutSeconds >= 0 && v.CommandTimeoutSeconds <= 10 && utf8.ValidString(v.ShellPrompt) && utf8.ValidString(v.AuditPrompt) && utf8.RuneCountInString(v.ShellPrompt) <= 32768 && utf8.RuneCountInString(v.AuditPrompt) <= 32768
}

type ProviderOptions struct {
	Protocol    string   `json:"protocol"`
	ProxyURL    string   `json:"proxyUrl"`
	Temperature *float64 `json:"temperature,omitempty"`
	InputRate   int64    `json:"inputMicrosPerThousand"`
	OutputRate  int64    `json:"outputMicrosPerThousand"`
}

func (v ProviderOptions) Valid() bool {
	if v.Protocol != "" && v.Protocol != "openai" && v.Protocol != "responses" && v.Protocol != "anthropic" {
		return false
	}
	if v.InputRate < 0 || v.InputRate > 1000000000 || v.OutputRate < 0 || v.OutputRate > 1000000000 {
		return false
	}
	return v.Temperature == nil || (*v.Temperature >= 0 && *v.Temperature <= 2)
}
func (s *Service) Provider(ctx context.Context, id string) (Profile, error) {
	if e := s.RequireExecution(ctx); e != nil {
		return Profile{}, e
	}
	p, encrypted, e := s.profile(ctx, id)
	if e != nil {
		return p, e
	}
	return s.openProfile(p, encrypted)
}
func (s *Service) SaveProviderOptions(ctx context.Context, id string, v ProviderOptions) error {
	if !v.Valid() {
		return fault.Invalid
	}
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `UPDATE ai_profiles SET config=$2 WHERE id=$1`, id, b)
	return e
}
