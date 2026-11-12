-- M05 owns host trust; M06 owns live leases; M08 owns durable access/recording/command evidence.
CREATE TABLE ssh_known_hosts (
 asset_id uuid PRIMARY KEY REFERENCES resource_registry(id), host text NOT NULL, port integer NOT NULL,
 public_key text NOT NULL, fingerprint text NOT NULL, version bigint NOT NULL DEFAULT 1,
 trusted_by uuid NOT NULL REFERENCES users(id), trusted_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE sessions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), actor_id uuid NOT NULL REFERENCES users(id), asset_id uuid NOT NULL REFERENCES resource_registry(id),
 asset_version bigint NOT NULL, credential_version bigint NOT NULL, host_key_version bigint NOT NULL,
 readonly boolean NOT NULL, state text NOT NULL CHECK(state IN ('reserved','connecting','connected','closed','failed','interrupted')),
 ticket_hash text NOT NULL, auth_hash text NOT NULL, ticket_expires_at timestamptz NOT NULL,
 lease_expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), connected_at timestamptz, closed_at timestamptz,
 close_reason text NOT NULL DEFAULT '', terminate_requested boolean NOT NULL DEFAULT false
);
CREATE INDEX sessions_live ON sessions(lease_expires_at) WHERE closed_at IS NULL;
CREATE INDEX sessions_actor ON sessions(actor_id,created_at DESC);
CREATE TABLE access_logs (
 session_id uuid PRIMARY KEY REFERENCES sessions(id), actor_id uuid NOT NULL REFERENCES users(id), asset_id uuid NOT NULL REFERENCES resource_registry(id),
 username text NOT NULL, asset_name text NOT NULL, target text NOT NULL, source_ip text NOT NULL,
 state text NOT NULL, reason text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(), ended_at timestamptz
);
CREATE TABLE recordings (
 session_id uuid PRIMARY KEY REFERENCES sessions(id), format text NOT NULL DEFAULT 'terminal-events-v1',
 state text NOT NULL DEFAULT 'writing', bytes bigint NOT NULL DEFAULT 0, events bigint NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(), ended_at timestamptz
);
CREATE TABLE recording_chunks (
 session_id uuid NOT NULL REFERENCES recordings(session_id), sequence bigint NOT NULL, elapsed_ms bigint NOT NULL,
 kind text NOT NULL CHECK(kind IN ('output','resize','exec-output')), payload bytea NOT NULL,
 PRIMARY KEY(session_id,sequence)
);
CREATE TABLE session_commands (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), session_id uuid NOT NULL REFERENCES sessions(id), actor_id uuid NOT NULL REFERENCES users(id),
 command text NOT NULL, source text NOT NULL DEFAULT 'explicit-exec', state text NOT NULL DEFAULT 'running', exit_code integer,
 created_at timestamptz NOT NULL DEFAULT now(), ended_at timestamptz
);
INSERT INTO permissions VALUES ('session.read','online-session','在线会话查看');
UPDATE instance_state SET schema_version=4;
