-- Preserve the previously enforced 15-minute failure window and lock duration.
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot || '{"login-lock-failed-duration":15,"login-lock-failed-times":5,"login-lock-account-duration":15}'::jsonb,
 ARRAY['login-lock-failed-duration','login-lock-failed-times','login-lock-account-duration']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id
 RETURNING id
)
UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
INSERT INTO settings(key,typed_value) VALUES
 ('login-lock-failed-duration','15'),('login-lock-failed-times','5'),('login-lock-account-duration','15');
ALTER TABLE login_locks ADD COLUMN configuration_revision bigint REFERENCES configuration_revisions(id);
UPDATE instance_state SET schema_version=63 WHERE singleton=1;
