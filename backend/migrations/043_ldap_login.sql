ALTER TABLE ldap_identity_providers ADD COLUMN enabled boolean NOT NULL DEFAULT false, ADD COLUMN binding_enabled boolean NOT NULL DEFAULT false;
CREATE TABLE ldap_identities (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), provider_id uuid NOT NULL REFERENCES ldap_identity_providers(id), user_id uuid NOT NULL REFERENCES users(id), subject text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(provider_id,subject), UNIQUE(provider_id,user_id));
ALTER TABLE auth_sessions ADD COLUMN ldap_identity_id uuid, ADD COLUMN ldap_provider_version bigint;
UPDATE instance_state SET schema_version=43 WHERE singleton=1;
