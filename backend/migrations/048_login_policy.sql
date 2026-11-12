CREATE TABLE identity_login_policy(singleton integer PRIMARY KEY CHECK(singleton=1),password_enabled boolean NOT NULL DEFAULT true,version bigint NOT NULL DEFAULT 1);
INSERT INTO identity_login_policy(singleton) VALUES(1);
CREATE TABLE identity_login_proofs(user_id uuid NOT NULL REFERENCES users(id),kind text NOT NULL,credential_id uuid NOT NULL,source_version bigint NOT NULL,config_version bigint NOT NULL,auth_epoch bigint NOT NULL,channel_version bigint NOT NULL DEFAULT 0,verified_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(user_id,kind,credential_id));
ALTER TABLE auth_sessions ADD COLUMN passkey_id uuid, ADD COLUMN contact_channel_version bigint;
UPDATE instance_state SET schema_version=48 WHERE singleton=1;
