CREATE TABLE oidc_identity_providers (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL,
 issuer text NOT NULL UNIQUE, client_id text NOT NULL, secret_sealed bytea,
 redirect_uri text NOT NULL, enabled boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE external_identities (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id),
 provider_id uuid NOT NULL REFERENCES oidc_identity_providers(id), issuer text NOT NULL,
 subject text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(issuer,subject), UNIQUE(user_id,provider_id)
);
UPDATE instance_state SET schema_version=38 WHERE singleton=1;
