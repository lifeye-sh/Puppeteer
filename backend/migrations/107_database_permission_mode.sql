ALTER TABLE database_assets ADD COLUMN permission_mode text NOT NULL DEFAULT 'account' CHECK (permission_mode IN ('account', 'program'));
ALTER TABLE database_assets DROP CONSTRAINT database_native_write_separate;
ALTER TABLE database_assets ADD CONSTRAINT database_native_write_separate CHECK
 (native_write_credential_id IS NULL OR (engine='mysql' AND (permission_mode='program' OR (native_write_credential_id<>readonly_credential_id AND native_write_credential_id IS DISTINCT FROM write_credential_id))));
UPDATE instance_state SET schema_version=107 WHERE singleton=1;
