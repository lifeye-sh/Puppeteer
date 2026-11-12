-- Preserve mandatory CAPTCHA on both upgrade and new installation.
UPDATE settings SET typed_value='true',revision=revision+1 WHERE key='login-captcha-enabled';
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||'{"login-captcha-enabled":true}'::jsonb,ARRAY['login-captcha-enabled']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1),enforced=enforced||'{"login-captcha-enabled":true}'::jsonb WHERE singleton=1;
UPDATE instance_state SET schema_version=102 WHERE singleton=1;
