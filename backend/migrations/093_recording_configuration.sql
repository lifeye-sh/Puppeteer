ALTER TABLE recordings ADD COLUMN enabled boolean NOT NULL DEFAULT true;
INSERT INTO settings(key,typed_value) VALUES('recording-enabled','true');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||'{"recording-enabled":true}'::jsonb,ARRAY['recording-enabled'] FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1),enforced=enforced||'{"recording-enabled":true}'::jsonb WHERE singleton=1;
UPDATE instance_state SET schema_version=93 WHERE singleton=1;
