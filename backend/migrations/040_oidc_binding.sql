ALTER TABLE oidc_identity_providers ADD COLUMN binding_enabled boolean NOT NULL DEFAULT false;
UPDATE instance_state SET schema_version=40 WHERE singleton=1;
