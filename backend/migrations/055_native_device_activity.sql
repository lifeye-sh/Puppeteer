-- Historical families imported by 054 have a known inventory registration
-- time, but no known last-use time. Do not manufacture activity during upgrade.
ALTER TABLE native_client_devices ALTER COLUMN last_seen_at DROP NOT NULL;
UPDATE native_client_devices SET last_seen_at=NULL
 WHERE registered_at=(SELECT applied_at FROM migration_history WHERE version=54)
 AND last_seen_at=registered_at;
UPDATE instance_state SET schema_version=55 WHERE singleton=1;
