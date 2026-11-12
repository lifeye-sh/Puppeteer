-- Preserve the existing third-party refresh family lifetime; native clients
-- retain their independent seven-day maximum in the issuance path.
INSERT INTO settings(key,typed_value) VALUES
 ('oidc-server-access-token-ttl','300'),
 ('oidc-server-refresh-token-ttl','2592000'),
 ('oidc-server-id-token-ttl','300'),
 ('oidc-server-auth-code-ttl','60');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||'{"oidc-server-access-token-ttl":300,"oidc-server-refresh-token-ttl":2592000,"oidc-server-id-token-ttl":300,"oidc-server-auth-code-ttl":60}'::jsonb,
 ARRAY['oidc-server-access-token-ttl','oidc-server-refresh-token-ttl','oidc-server-id-token-ttl','oidc-server-auth-code-ttl']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1) WHERE singleton=1;
UPDATE instance_state SET schema_version=90 WHERE singleton=1;
