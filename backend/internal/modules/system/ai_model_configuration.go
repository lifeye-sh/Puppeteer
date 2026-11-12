package system

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/modelendpoint"
	"strings"
)

var aiModelKeys = []string{"llm-api-key", "llm-base-url", "llm-model", "llm-temperature", "llm-max-tokens", "llm-proxy-url", "llm-profile-enabled", "llm-allow-private", "llm-protocol", "llm-input-rate", "llm-output-rate"}

func validateAIModelConfiguration(v ConfigurationValues) error {
	protocol := v.String("llm-protocol")
	if protocol != "openai" && protocol != "responses" && protocol != "anthropic" {
		return fault.Invalid
	}
	base, model := v.String("llm-base-url"), v.String("llm-model")
	if proxy := v.String("llm-proxy-url"); proxy != "" {
		u, e := modelendpoint.ParseProxy(proxy, v.Bool("llm-allow-private"))
		if e != nil || (u.Path != "" && u.Path != "/") {
			return fault.Invalid
		}
	}
	if base == "" && model == "" && !v.Bool("llm-profile-enabled") {
		return nil
	}
	if strings.TrimSpace(model) == "" || strings.TrimSpace(model) != model {
		return fault.Invalid
	}
	if _, e := modelendpoint.Parse(base, v.Bool("llm-allow-private")); e != nil {
		return fault.Invalid
	}
	return nil
}

func (s *Service) applyAIModelConfiguration(ctx context.Context, v ConfigurationValues) error {
	if _, e := s.db.Exec(ctx, `SELECT singleton FROM ai_settings FOR UPDATE`); e != nil {
		return e
	}
	var exists bool
	if e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM ai_profiles WHERE settings_managed)`).Scan(&exists); e != nil {
		return e
	}
	if !exists && v.String("llm-base-url") == "" && v.String("llm-model") == "" && !v.Bool("llm-profile-enabled") {
		return nil
	}
	if !exists {
		var count int
		if e := s.db.Row(ctx, `SELECT count(*) FROM ai_profiles WHERE deleted_at IS NULL`).Scan(&count); e != nil {
			return e
		}
		if count >= 50 {
			return fault.Conflict
		}
	}
	options := map[string]any{"protocol": v.String("llm-protocol"), "proxyUrl": v.String("llm-proxy-url"), "inputMicrosPerThousand": v.Int("llm-input-rate"), "outputMicrosPerThousand": v.Int("llm-output-rate")}
	if temperature := v.Float("llm-temperature"); temperature >= 0 {
		options["temperature"] = temperature
	}
	config, e := json.Marshal(options)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO ai_profiles(name,base_url,model,secret,allow_private,enabled,max_output_tokens,config,settings_managed,configuration_secret_id) VALUES('平台默认模型',$1,$2,''::bytea,$3,$4,$5,$6,true,NULLIF($7,'')::uuid) ON CONFLICT(settings_managed) WHERE settings_managed DO UPDATE SET base_url=EXCLUDED.base_url,model=EXCLUDED.model,allow_private=EXCLUDED.allow_private,enabled=EXCLUDED.enabled,max_output_tokens=EXCLUDED.max_output_tokens,config=EXCLUDED.config,configuration_secret_id=EXCLUDED.configuration_secret_id,version=ai_profiles.version+1,deleted_at=NULL`, v.String("llm-base-url"), v.String("llm-model"), v.Bool("llm-allow-private"), v.Bool("llm-profile-enabled"), v.Int("llm-max-tokens"), config, v.String("llm-api-key"))
	return e
}
