ALTER TABLE native_ssh_sessions ALTER COLUMN key_id DROP NOT NULL;
ALTER TABLE native_ssh_sessions ADD COLUMN auth_method text NOT NULL DEFAULT 'key'
 CHECK (auth_method IN ('key','password'));
ALTER TABLE native_ssh_sessions ADD CONSTRAINT native_ssh_auth_source
 CHECK ((auth_method='key' AND key_id IS NOT NULL) OR (auth_method='password' AND key_id IS NULL));
-- Preserve the existing key-only boundary on both upgrades and new installs.
INSERT INTO settings(key,typed_value) VALUES ('ssh-server-disable-password-auth','true');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||'{"ssh-server-disable-password-auth":true}'::jsonb,ARRAY['ssh-server-disable-password-auth']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1),enforced=enforced||'{"ssh-server-disable-password-auth":true}'::jsonb WHERE singleton=1;
UPDATE instance_state SET schema_version=99 WHERE singleton=1;
