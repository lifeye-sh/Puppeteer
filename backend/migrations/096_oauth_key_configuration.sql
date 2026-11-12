CREATE TABLE oauth_hmac_keys(id uuid PRIMARY KEY,sealed bytea NOT NULL,active boolean NOT NULL DEFAULT true,created_at timestamptz NOT NULL DEFAULT now());
CREATE UNIQUE INDEX oauth_hmac_one_active ON oauth_hmac_keys(active) WHERE active;
ALTER TABLE oauth_pending_authorizations ADD COLUMN hmac_key_id uuid REFERENCES oauth_hmac_keys(id),ADD COLUMN hmac_tag bytea,
 ADD CONSTRAINT oauth_pending_authorizations_hmac_pair CHECK ((hmac_key_id IS NULL AND hmac_tag IS NULL) OR (hmac_key_id IS NOT NULL AND hmac_tag IS NOT NULL AND octet_length(hmac_tag)=32));
ALTER TABLE oauth_codes ADD COLUMN hmac_key_id uuid REFERENCES oauth_hmac_keys(id),ADD COLUMN hmac_tag bytea,
 ADD CONSTRAINT oauth_codes_hmac_pair CHECK ((hmac_key_id IS NULL AND hmac_tag IS NULL) OR (hmac_key_id IS NOT NULL AND hmac_tag IS NOT NULL AND octet_length(hmac_tag)=32));
ALTER TABLE oauth_tokens ADD COLUMN hmac_key_id uuid REFERENCES oauth_hmac_keys(id),ADD COLUMN hmac_tag bytea,
 ADD CONSTRAINT oauth_tokens_hmac_pair CHECK ((hmac_key_id IS NULL AND hmac_tag IS NULL) OR (hmac_key_id IS NOT NULL AND hmac_tag IS NOT NULL AND octet_length(hmac_tag)=32));

INSERT INTO settings(key,typed_value) VALUES('oidc-server-signing-key','""'),('oidc-server-hmac-secret','""');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||'{"oidc-server-signing-key":"","oidc-server-hmac-secret":""}'::jsonb,ARRAY['oidc-server-signing-key','oidc-server-hmac-secret'] FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1) WHERE singleton=1;
UPDATE instance_state SET schema_version=96 WHERE singleton=1;
