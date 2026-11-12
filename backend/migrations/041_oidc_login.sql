CREATE TABLE oidc_login_authorizations (
 state_hash text PRIMARY KEY, binding_hash text NOT NULL,
 provider_id uuid NOT NULL REFERENCES oidc_identity_providers(id), provider_version bigint NOT NULL,
 challenge_sealed bytea NOT NULL, auth_epoch bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL
);
CREATE INDEX oidc_login_authorizations_expiry ON oidc_login_authorizations(expires_at);
ALTER TABLE auth_sessions ADD COLUMN external_identity_id uuid;
ALTER TABLE auth_sessions ADD COLUMN oidc_provider_version bigint;
ALTER TABLE users ADD COLUMN external_login_valid_after timestamptz NOT NULL DEFAULT '-infinity';
UPDATE instance_state SET schema_version=41 WHERE singleton=1;
