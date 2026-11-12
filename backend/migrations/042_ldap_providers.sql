CREATE TABLE ldap_identity_providers (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL,
 url text NOT NULL, base_dn text NOT NULL, bind_dn text NOT NULL,
 search_filter text NOT NULL, login_attribute text NOT NULL, identity_attribute text NOT NULL,
 ca_pem text NOT NULL DEFAULT '', secret_sealed bytea,
 version bigint NOT NULL DEFAULT 1, created_at timestamptz NOT NULL DEFAULT now()
);
UPDATE instance_state SET schema_version=42 WHERE singleton=1;
