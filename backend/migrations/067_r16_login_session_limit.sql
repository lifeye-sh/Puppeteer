-- Existing installations had no browser-session count cap. Preserve that
-- behavior until an administrator explicitly enables the new control.
INSERT INTO settings(key,typed_value) VALUES
 ('login-session-count-custom','false'),('login-session-count-limit','5');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot || (SELECT jsonb_object_agg(key,typed_value) FROM settings WHERE key IN ('login-session-count-custom','login-session-count-limit')),
 ARRAY['login-session-count-custom','login-session-count-limit']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
)
UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
CREATE INDEX auth_sessions_active_count ON auth_sessions(user_id,auth_epoch,absolute_expires_at) WHERE revoked_at IS NULL AND phase='authenticated';
UPDATE instance_state SET schema_version=67 WHERE singleton=1;
