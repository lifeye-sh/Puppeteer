ALTER TABLE sessions ADD COLUMN native_forward boolean NOT NULL DEFAULT false;
CREATE TABLE native_forward_settings (
 singleton integer PRIMARY KEY CHECK(singleton=1),
 enabled boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1,
 rules jsonb NOT NULL DEFAULT '[]'
);
INSERT INTO native_forward_settings(singleton) VALUES(1);
CREATE TABLE native_forward_streams (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 host text NOT NULL,
 port integer NOT NULL,
 address text NOT NULL,
 state text NOT NULL DEFAULT 'connecting',
 bytes_to_target bigint NOT NULL DEFAULT 0,
 bytes_from_target bigint NOT NULL DEFAULT 0,
 started_at timestamptz NOT NULL DEFAULT now(),
 ended_at timestamptz
);
CREATE INDEX ON native_forward_streams(session_id);
UPDATE instance_state SET schema_version=52 WHERE singleton=1;
