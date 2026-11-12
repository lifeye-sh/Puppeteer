CREATE TABLE oidc_authorizations (
 state_hash text PRIMARY KEY, binding_hash text NOT NULL,
 user_id uuid NOT NULL REFERENCES users(id), provider_id uuid NOT NULL REFERENCES oidc_identity_providers(id),
 provider_version bigint NOT NULL, purpose text NOT NULL CHECK (purpose IN ('test','bind')),
 challenge_sealed bytea NOT NULL, auth_epoch bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL
);
CREATE INDEX oidc_authorizations_expiry ON oidc_authorizations(expires_at);
UPDATE instance_state SET schema_version=39 WHERE singleton=1;
