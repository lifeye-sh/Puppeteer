CREATE TABLE session_shares (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), session_id uuid NOT NULL REFERENCES sessions(id),
 creator_id uuid NOT NULL REFERENCES users(id), recipient_id uuid NOT NULL REFERENCES users(id),
 token_hash text NOT NULL UNIQUE, expires_at timestamptz NOT NULL,
 claimed_at timestamptz, revoked_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX session_shares_session ON session_shares(session_id);
CREATE TABLE session_observers (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), session_id uuid NOT NULL REFERENCES sessions(id),
 actor_id uuid NOT NULL REFERENCES users(id), auth_hash text NOT NULL,
 share_id uuid REFERENCES session_shares(id), expires_at timestamptz NOT NULL,
 heartbeat_expires_at timestamptz NOT NULL DEFAULT now()+interval '30 seconds',
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX session_observers_session ON session_observers(session_id);
UPDATE instance_state SET schema_version=19;
