CREATE TABLE native_ssh_gateway_keys (
 singleton integer PRIMARY KEY CHECK(singleton=1),
 private_key bytea NOT NULL,
 public_key text NOT NULL
);
CREATE TABLE native_ssh_sessions (
 session_id uuid PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
 key_id uuid NOT NULL REFERENCES personal_ssh_keys(id) ON DELETE CASCADE,
 user_version bigint NOT NULL,
 auth_epoch bigint NOT NULL,
 server_version bigint NOT NULL,
 mfa_digest text NOT NULL,
 mfa_verified boolean NOT NULL
);
UPDATE instance_state SET schema_version=51 WHERE singleton=1;
