-- An empty RP/origin pair inherits the explicitly configured deployment identity.
-- The allow switch preserves existing deployments; without a valid RP it opens no login.
INSERT INTO settings(key,typed_value) VALUES('passkey-enabled','true'),('passkey-domain','""'),('passkey-origins','"[]"');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||'{"passkey-enabled":true,"passkey-domain":"","passkey-origins":"[]"}'::jsonb,ARRAY['passkey-enabled','passkey-domain','passkey-origins'] FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1),enforced=enforced||'{"passkey-enabled":true}'::jsonb WHERE singleton=1;
UPDATE instance_state SET schema_version=89 WHERE singleton=1;
