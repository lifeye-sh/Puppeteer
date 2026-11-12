ALTER TABLE sessions ADD COLUMN recording_purged_at timestamptz;
INSERT INTO settings(key,typed_value) SELECT 'session-saved-limit-days',typed_value FROM settings WHERE key='access-log-saved-limit-days';
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||jsonb_build_object('session-saved-limit-days',r.snapshot->'access-log-saved-limit-days'),ARRAY['session-saved-limit-days'] FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1) WHERE singleton=1;
UPDATE instance_state SET schema_version=94 WHERE singleton=1;
