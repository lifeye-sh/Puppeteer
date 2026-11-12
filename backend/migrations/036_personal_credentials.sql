CREATE TABLE personal_api_keys (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id),
 name text NOT NULL, token_hash text NOT NULL UNIQUE, prefix text NOT NULL,
 scopes text[] NOT NULL, auth_epoch bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL,
 last_used_at timestamptz, revoked_at timestamptz
);
CREATE INDEX personal_api_keys_owner ON personal_api_keys(user_id,created_at DESC);
CREATE TABLE personal_ssh_keys (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id),
 name text NOT NULL, public_key text NOT NULL, fingerprint text NOT NULL, algorithm text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL, revoked_at timestamptz
);
CREATE UNIQUE INDEX personal_ssh_keys_active ON personal_ssh_keys(user_id,fingerprint) WHERE revoked_at IS NULL;
UPDATE instance_state SET schema_version=36 WHERE singleton=1;
