-- F00 migration_history is created by the runner.
-- M18: instance and initial typed setting registry.
CREATE TABLE instance_state (
  singleton integer PRIMARY KEY CHECK (singleton=1),
  instance_id uuid NOT NULL DEFAULT gen_random_uuid(),
  initialized_at timestamptz,
  schema_version integer NOT NULL DEFAULT 1,
  auth_epoch bigint NOT NULL DEFAULT 1
);
INSERT INTO instance_state(singleton) VALUES(1);
CREATE TABLE settings (
  key text PRIMARY KEY,
  typed_value jsonb NOT NULL,
  revision bigint NOT NULL DEFAULT 1
);
INSERT INTO settings(key,typed_value) VALUES
 ('system-name','"Next Terminal · 新后端"'),
 ('login-session-duration','480'),
 ('login-captcha-enabled','false');
-- M01: identity and persistent, revocable opaque sessions.
CREATE TABLE users (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 username text NOT NULL UNIQUE,
 nickname text NOT NULL,
 password_hash text NOT NULL,
 type text NOT NULL CHECK(type IN ('admin','user')),
 status text NOT NULL DEFAULT 'enabled' CHECK(status IN ('enabled','disabled')),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE auth_sessions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 token_hash text NOT NULL UNIQUE,
 user_id uuid NOT NULL REFERENCES users(id),
 auth_epoch bigint NOT NULL,
 issued_at timestamptz NOT NULL DEFAULT now(),
 absolute_expires_at timestamptz NOT NULL,
 revoked_at timestamptz,
 source_ip text NOT NULL
);
CREATE INDEX auth_sessions_user ON auth_sessions(user_id);
CREATE INDEX auth_sessions_expiry ON auth_sessions(absolute_expires_at) WHERE revoked_at IS NULL;
-- M08: append-only through the service; secrets and raw request bodies excluded.
CREATE TABLE operation_logs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 actor_id uuid REFERENCES users(id),
 action text NOT NULL,
 outcome text NOT NULL,
 request_id text NOT NULL,
 source_ip text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE login_logs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 actor_id uuid REFERENCES users(id),
 outcome text NOT NULL,
 request_id text NOT NULL,
 source_ip text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX operation_logs_recent ON operation_logs(created_at DESC);
CREATE INDEX login_logs_recent ON login_logs(created_at DESC);
