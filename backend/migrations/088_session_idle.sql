ALTER TABLE sessions ADD COLUMN last_activity_at timestamptz NOT NULL DEFAULT clock_timestamp();
INSERT INTO settings(key,typed_value) VALUES('access-max-idle-second','900');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||'{"access-max-idle-second":900}'::jsonb,ARRAY['access-max-idle-second'] FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1),enforced=enforced||'{"access-max-idle-second":900}'::jsonb WHERE singleton=1;
UPDATE instance_state SET schema_version=88 WHERE singleton=1;
