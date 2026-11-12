-- Additive R04 audit extension; existing migrations and historic records stay intact.
ALTER TABLE session_commands ADD COLUMN note text NOT NULL DEFAULT '';
ALTER TABLE session_commands ADD COLUMN sequence bigint GENERATED ALWAYS AS IDENTITY;
ALTER TABLE recordings ADD COLUMN command_count integer NOT NULL DEFAULT 0;
UPDATE recordings r SET command_count=(SELECT count(*) FROM session_commands c WHERE c.session_id=r.session_id);
CREATE INDEX session_commands_history ON session_commands(session_id,created_at DESC,sequence DESC);
UPDATE instance_state SET schema_version=5;
