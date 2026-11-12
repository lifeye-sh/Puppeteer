INSERT INTO settings(key,typed_value)
SELECT key,value FROM ai_settings a CROSS JOIN LATERAL jsonb_each(jsonb_build_object(
 'llm-enabled',a.enabled,
 'llm-shell-enabled',coalesce((a.config->>'shellEnabled')::boolean,false),
 'llm-audit-enabled',coalesce((a.config->>'auditEnabled')::boolean,false),
 'llm-shell-prompt',coalesce(a.config->>'shellPrompt',''),
 'llm-audit-prompt',coalesce(a.config->>'auditPrompt',''))) WHERE a.singleton;
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||(SELECT jsonb_object_agg(key,typed_value) FROM settings WHERE key LIKE 'llm-%'),ARRAY['llm-enabled','llm-shell-enabled','llm-audit-enabled','llm-shell-prompt','llm-audit-prompt'] FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1) WHERE singleton=1;
UPDATE instance_state SET schema_version=83 WHERE singleton=1;
