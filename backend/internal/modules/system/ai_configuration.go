package system

import "context"

// Preserve budgets, readonly-context permission and all independent model
// profiles. This projection only owns the five global assistant properties.
func (s *Service) applyAIConfiguration(ctx context.Context, v ConfigurationValues) error {
	_, e := s.db.Exec(ctx, `UPDATE ai_settings SET enabled=$1,config=config||jsonb_build_object('shellEnabled',$2::boolean,'auditEnabled',$3::boolean,'shellPrompt',$4::text,'auditPrompt',$5::text),version=version+1 WHERE singleton AND (enabled IS DISTINCT FROM $1 OR coalesce((config->>'shellEnabled')::boolean,false) IS DISTINCT FROM $2 OR coalesce((config->>'auditEnabled')::boolean,false) IS DISTINCT FROM $3 OR coalesce(config->>'shellPrompt','') IS DISTINCT FROM $4 OR coalesce(config->>'auditPrompt','') IS DISTINCT FROM $5)`, v.Bool("llm-enabled"), v.Bool("llm-shell-enabled"), v.Bool("llm-audit-enabled"), v.String("llm-shell-prompt"), v.String("llm-audit-prompt"))
	return e
}
