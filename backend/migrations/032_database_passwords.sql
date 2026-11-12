CREATE TABLE database_passwords (
 user_id uuid PRIMARY KEY REFERENCES users(id),
 ciphertext bytea NOT NULL, wrapped_key bytea NOT NULL,
 version bigint NOT NULL DEFAULT 1,
 expires_at timestamptz NOT NULL, revoked_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now()
);
UPDATE instance_state SET schema_version=32 WHERE singleton=1;
