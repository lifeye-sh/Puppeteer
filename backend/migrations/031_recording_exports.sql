CREATE TABLE recording_exports (
 session_id uuid PRIMARY KEY REFERENCES recordings(session_id) ON DELETE CASCADE,
 job_id uuid NOT NULL REFERENCES jobs(id),
 actor_id uuid NOT NULL REFERENCES users(id),
 progress integer NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 100),
 attempts integer NOT NULL DEFAULT 0,
 size bigint NOT NULL DEFAULT 0,
 sha256 text NOT NULL DEFAULT '',
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX recording_exports_job ON recording_exports(job_id);
UPDATE instance_state SET schema_version=31 WHERE singleton=1;
