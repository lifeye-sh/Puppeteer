ALTER TABLE assets DROP CONSTRAINT assets_protocol_check;
ALTER TABLE assets ADD CONSTRAINT assets_protocol_check CHECK(protocol IN ('ssh','telnet','rdp','vnc'));
ALTER TABLE assets ADD COLUMN cert_fingerprint text NOT NULL DEFAULT '';
ALTER TABLE sessions DROP CONSTRAINT sessions_protocol_check;
ALTER TABLE sessions ADD CONSTRAINT sessions_protocol_check CHECK(protocol IN ('ssh','telnet','rdp','vnc'));
ALTER TABLE recording_chunks DROP CONSTRAINT recording_chunks_kind_check;
ALTER TABLE recording_chunks ADD CONSTRAINT recording_chunks_kind_check CHECK(kind IN ('output','resize','exec-output','graphics'));
UPDATE instance_state SET schema_version=18;
