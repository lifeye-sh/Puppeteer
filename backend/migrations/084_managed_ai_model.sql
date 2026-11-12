ALTER TABLE ai_profiles ADD COLUMN settings_managed boolean NOT NULL DEFAULT false;
ALTER TABLE ai_profiles ADD COLUMN configuration_secret_id uuid REFERENCES configuration_secrets(id);
CREATE UNIQUE INDEX ai_profiles_managed ON ai_profiles(settings_managed) WHERE settings_managed;
INSERT INTO settings(key,typed_value) VALUES
('llm-api-key','""'),('llm-base-url','""'),('llm-model','""'),('llm-temperature','-1'),('llm-max-tokens','1024'),('llm-proxy-url','""'),
('llm-profile-enabled','false'),('llm-allow-private','false'),('llm-protocol','"openai"'),('llm-input-rate','0'),('llm-output-rate','0');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||(SELECT jsonb_object_agg(key,typed_value) FROM settings WHERE key IN ('llm-api-key','llm-base-url','llm-model','llm-temperature','llm-max-tokens','llm-proxy-url','llm-profile-enabled','llm-allow-private','llm-protocol','llm-input-rate','llm-output-rate')),ARRAY['llm-api-key','llm-base-url','llm-model','llm-temperature','llm-max-tokens','llm-proxy-url','llm-profile-enabled','llm-allow-private','llm-protocol','llm-input-rate','llm-output-rate'] FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1) WHERE singleton=1;
UPDATE instance_state SET schema_version=84 WHERE singleton=1;
