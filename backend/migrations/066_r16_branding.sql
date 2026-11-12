INSERT INTO settings(key,typed_value) VALUES ('system-copyright','"运维管理平台"'),('system-icp','""') ON CONFLICT DO NOTHING;
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot || (SELECT jsonb_object_agg(key,typed_value) FROM settings WHERE key IN ('system-name','system-copyright','system-icp')),
 ARRAY['system-name','system-copyright','system-icp']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
)
UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE instance_state SET schema_version=66 WHERE singleton=1;
