INSERT INTO settings(key,typed_value)
 SELECT 'oidc-server-enabled',to_jsonb(enabled) FROM oauth_server_settings WHERE singleton=1
 UNION ALL SELECT 'oidc-server-issuer',to_jsonb(issuer) FROM oauth_server_settings WHERE singleton=1
 UNION ALL SELECT 'native-client-enabled',to_jsonb(native_enabled) FROM oauth_server_settings WHERE singleton=1;
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||(SELECT jsonb_object_agg(key,typed_value) FROM settings WHERE key IN ('oidc-server-enabled','oidc-server-issuer','native-client-enabled')),
 ARRAY['oidc-server-enabled','oidc-server-issuer','native-client-enabled']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1),enforced=enforced||(SELECT jsonb_object_agg(key,typed_value) FROM settings WHERE key IN ('oidc-server-enabled','native-client-enabled')) WHERE singleton=1;
UPDATE instance_state SET schema_version=95 WHERE singleton=1;
