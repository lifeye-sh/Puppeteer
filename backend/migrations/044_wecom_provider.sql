ALTER TABLE oidc_identity_providers ADD COLUMN kind text NOT NULL DEFAULT 'oidc' CHECK(kind IN ('oidc','wecom')), ADD COLUMN agent_id text NOT NULL DEFAULT '', ADD COLUMN authorization_url text NOT NULL DEFAULT '';
UPDATE instance_state SET schema_version=44 WHERE singleton=1;
