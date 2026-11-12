-- Preserve all existing quotas and issued certificates. Defaults affect only new objects.
INSERT INTO settings(key,typed_value) VALUES
 ('user-default-storage-size','5368709120'),
 ('user-client-cert-valid-days','90');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot || '{"user-default-storage-size":5368709120,"user-client-cert-valid-days":90}'::jsonb,
 ARRAY['user-default-storage-size','user-client-cert-valid-days']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
)
UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE instance_state SET schema_version=71 WHERE singleton=1;
