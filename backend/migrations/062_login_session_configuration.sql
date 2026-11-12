-- Extend the current snapshot without rewriting historical versions.
DO $$
DECLARE duration jsonb; next_revision bigint;
BEGIN
 SELECT typed_value INTO duration FROM settings WHERE key='login-session-duration';
 IF duration IS NULL OR jsonb_typeof(duration)<>'number' OR duration::text !~ '^[0-9]+$'
    OR (duration::text)::numeric NOT BETWEEN 1 AND 525600 THEN
   RAISE EXCEPTION 'Invalid existing login-session-duration; expected integer minutes 1..525600';
 END IF;
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot || jsonb_build_object('login-session-duration',duration),ARRAY['login-session-duration']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id
 RETURNING id INTO next_revision;
 UPDATE configuration_head SET revision=next_revision WHERE singleton=1;
END $$;
ALTER TABLE auth_sessions ADD COLUMN configuration_revision bigint REFERENCES configuration_revisions(id);
UPDATE instance_state SET schema_version=62 WHERE singleton=1;
