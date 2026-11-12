ALTER TABLE ldap_identities ADD COLUMN managed boolean NOT NULL DEFAULT false;
CREATE TABLE ldap_sync_previews(token_hash text PRIMARY KEY,user_id uuid NOT NULL REFERENCES users(id),session_hash text NOT NULL,provider_id uuid NOT NULL REFERENCES ldap_identity_providers(id),provider_version bigint NOT NULL,auth_epoch bigint NOT NULL,entries jsonb NOT NULL,expires_at timestamptz NOT NULL);
UPDATE instance_state SET schema_version=47 WHERE singleton=1;
