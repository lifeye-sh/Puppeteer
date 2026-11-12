ALTER TABLE auth_sessions ADD COLUMN mfa_verified_at timestamptz;
INSERT INTO settings(key,typed_value) VALUES('login-force-totp-enabled','false');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot || '{"login-force-totp-enabled":false}'::jsonb,ARRAY['login-force-totp-enabled']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
)
UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE instance_state SET schema_version=76 WHERE singleton=1;
