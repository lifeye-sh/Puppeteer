CREATE TABLE passkey_credentials (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id),
 rp_id text NOT NULL, credential_id bytea NOT NULL, name text NOT NULL,
 credential jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 last_used_at timestamptz, revoked_at timestamptz,
 UNIQUE(rp_id,credential_id)
);
CREATE INDEX passkey_credentials_owner ON passkey_credentials(user_id);
CREATE TABLE passkey_ceremonies (
 token_hash text PRIMARY KEY, binding_hash text NOT NULL,
 user_id uuid REFERENCES users(id), purpose text NOT NULL CHECK(purpose IN ('register','login')),
 session_data jsonb NOT NULL, auth_epoch bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL,
 consumed_at timestamptz
);
CREATE INDEX passkey_ceremonies_expiry ON passkey_ceremonies(expires_at);
UPDATE instance_state SET schema_version=37 WHERE singleton=1;
