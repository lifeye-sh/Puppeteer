-- Existing passwords start their policy clock at upgrade; the true historic
-- change date was not recorded. Expiration remains disabled by default.
ALTER TABLE users ADD COLUMN password_changed_at timestamptz NOT NULL DEFAULT now();
CREATE FUNCTION track_password_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.password_hash IS DISTINCT FROM OLD.password_hash THEN NEW.password_changed_at=now(); END IF;
 RETURN NEW;
END
$$;
CREATE TRIGGER users_password_changed BEFORE UPDATE OF password_hash ON users FOR EACH ROW EXECUTE FUNCTION track_password_change();
ALTER TABLE auth_sessions DROP CONSTRAINT auth_sessions_phase_check;
ALTER TABLE auth_sessions ADD CONSTRAINT auth_sessions_phase_check CHECK(phase IN ('authenticated','mfa_pending','mfa_enroll','password_change'));
INSERT INTO settings(key,typed_value) VALUES('password-expiration-period','-1');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot || '{"password-expiration-period":-1}'::jsonb,ARRAY['password-expiration-period']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
)
UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE instance_state SET schema_version=75 WHERE singleton=1;
