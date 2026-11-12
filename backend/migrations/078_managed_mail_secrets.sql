CREATE TABLE configuration_secrets (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 key text NOT NULL,
 sealed bytea NOT NULL,
 created_by uuid REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE notification_channels ADD COLUMN settings_managed boolean NOT NULL DEFAULT false;
ALTER TABLE notification_channels ADD COLUMN configuration_secret_id uuid REFERENCES configuration_secrets(id);
CREATE UNIQUE INDEX notification_managed_kind ON notification_channels(type) WHERE settings_managed;
INSERT INTO settings(key,typed_value) VALUES
 ('mail-enabled','false'),('mail-use-ssl','true'),('mail-host','""'),('mail-port','465'),
 ('mail-username','""'),('mail-password','""'),('mail-insecure-skip-verify','false'),('mail-from','""'),('mail-ca-pem','""');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot || '{"mail-enabled":false,"mail-use-ssl":true,"mail-host":"","mail-port":465,"mail-username":"","mail-password":"","mail-insecure-skip-verify":false,"mail-from":"","mail-ca-pem":""}'::jsonb,
 ARRAY['mail-enabled','mail-use-ssl','mail-host','mail-port','mail-username','mail-password','mail-insecure-skip-verify','mail-from','mail-ca-pem']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
)
UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE instance_state SET schema_version=78 WHERE singleton=1;
