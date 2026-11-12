CREATE TABLE database_sessions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id), asset_id uuid NOT NULL REFERENCES database_assets(id),
 password_version bigint NOT NULL, asset_version bigint NOT NULL, remote_address text NOT NULL,
 started_at timestamptz NOT NULL DEFAULT now(), ended_at timestamptz, end_reason text NOT NULL DEFAULT ''
);
CREATE INDEX database_sessions_live ON database_sessions(user_id) WHERE ended_at IS NULL;
CREATE TABLE database_sql_logs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), session_id uuid REFERENCES database_sessions(id),
 sql_text text NOT NULL CHECK(octet_length(sql_text)<=1048576), sql_sha256 text NOT NULL, command integer NOT NULL,
 parameter_meta jsonb NOT NULL DEFAULT '[]',sql_sanitized boolean NOT NULL DEFAULT false,
 state text NOT NULL DEFAULT 'running' CHECK(state IN ('running','completed','failed','unknown','rolled_back')),
 row_count bigint NOT NULL DEFAULT 0,duration_ms bigint NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(),finished_at timestamptz
);
CREATE INDEX database_sql_logs_created ON database_sql_logs(created_at DESC,id);
UPDATE instance_state SET schema_version=34 WHERE singleton=1;
