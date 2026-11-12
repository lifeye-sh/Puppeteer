-- Preserve existing protection and cookie behavior. IP locking is opt-in because
-- deployments behind a shared proxy must establish trustworthy source IPs first.
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot || '{"login-lock-enabled":true,"login-lock-ip-duration":0,"login-session-browser-close-logout":false}'::jsonb,
 ARRAY['login-lock-enabled','login-lock-ip-duration','login-session-browser-close-logout']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
)
UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
INSERT INTO settings(key,typed_value) VALUES
 ('login-lock-enabled','true'),('login-lock-ip-duration','0'),('login-session-browser-close-logout','false');
ALTER TABLE auth_sessions ADD COLUMN browser_session boolean NOT NULL DEFAULT false;
CREATE TABLE login_ip_locks (
 address inet PRIMARY KEY,
 failures integer NOT NULL CHECK(failures>0),
 locked_until timestamptz,
 last_failed_at timestamptz NOT NULL,
 configuration_revision bigint NOT NULL REFERENCES configuration_revisions(id)
);
UPDATE instance_state SET schema_version=64 WHERE singleton=1;
