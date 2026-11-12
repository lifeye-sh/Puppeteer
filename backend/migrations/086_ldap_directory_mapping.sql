ALTER TABLE ldap_identity_providers
    ADD COLUMN search_size_limit integer NOT NULL DEFAULT 10000 CHECK (search_size_limit BETWEEN 1 AND 10000),
    ADD COLUMN property_mapping jsonb;
UPDATE instance_state SET schema_version=86 WHERE singleton=1;
