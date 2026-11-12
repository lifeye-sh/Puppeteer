ALTER TABLE assets DROP CONSTRAINT assets_protocol_check;
ALTER TABLE assets ADD CONSTRAINT assets_protocol_check CHECK(protocol IN ('ssh','telnet'));
ALTER TABLE sessions ADD COLUMN protocol text NOT NULL DEFAULT 'ssh' CHECK(protocol IN ('ssh','telnet'));
UPDATE instance_state SET schema_version=17;
