INSERT INTO settings(key,typed_value) VALUES('access-retention-days','31');
ALTER TABLE sessions ADD COLUMN audit_purged_at timestamptz;
CREATE INDEX sessions_retention ON sessions(closed_at,id) WHERE audit_purged_at IS NULL AND closed_at IS NOT NULL;
UPDATE instance_state SET schema_version=29;
