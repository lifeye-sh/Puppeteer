ALTER TABLE ldap_identity_providers ADD COLUMN settings_managed boolean NOT NULL DEFAULT false;
ALTER TABLE ldap_identity_providers ADD COLUMN configuration_secret_id uuid REFERENCES configuration_secrets(id);
CREATE UNIQUE INDEX ldap_providers_managed ON ldap_identity_providers(settings_managed) WHERE settings_managed;
INSERT INTO settings(key,typed_value) VALUES
('ldap-enabled','false'),('ldap-url','""'),('ldap-user','""'),('ldap-password','""'),('ldap-base-dn','""'),
('ldap-user-search-size-limit','1000'),('ldap-user-search-filter','"(objectClass=inetOrgPerson)"'),
('ldap-user-property-mapping','"{\"username\":\"uid\",\"nickname\":\"cn\",\"mail\":\"mail\"}"'),
('ldap-identity-attribute','"entryUUID"'),('ldap-ca-pem','""'),('ldap-binding-enabled','false');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||(SELECT jsonb_object_agg(key,typed_value) FROM settings WHERE key LIKE 'ldap-%'),ARRAY['ldap-enabled','ldap-url','ldap-user','ldap-password','ldap-base-dn','ldap-user-search-size-limit','ldap-user-search-filter','ldap-user-property-mapping','ldap-identity-attribute','ldap-ca-pem','ldap-binding-enabled'] FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1),enforced=enforced||'{"ldap-enabled":false,"ldap-binding-enabled":false}'::jsonb WHERE singleton=1;
UPDATE instance_state SET schema_version=87 WHERE singleton=1;
