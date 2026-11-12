-- New reservations capture the revision and metadata. Existing sessions retain
-- their previous appearance; no fabricated historical watermark is added.
ALTER TABLE sessions ADD COLUMN watermark jsonb NOT NULL DEFAULT '{}';
INSERT INTO settings(key,typed_value) VALUES
 ('watermark-enabled','true'),
 ('watermark-content','"受控访问"'),
 ('watermark-content-asset-username','false'),
 ('watermark-content-user-account','true'),
 ('watermark-font-color','"rgba(128, 128, 128, 0.3)"'),
 ('watermark-font-size','16');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot || '{"watermark-enabled":true,"watermark-content":"受控访问","watermark-content-asset-username":false,"watermark-content-user-account":true,"watermark-font-color":"rgba(128, 128, 128, 0.3)","watermark-font-size":16}'::jsonb,
 ARRAY['watermark-enabled','watermark-content','watermark-content-asset-username','watermark-content-user-account','watermark-font-color','watermark-font-size']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
)
UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE instance_state SET schema_version=73 WHERE singleton=1;
