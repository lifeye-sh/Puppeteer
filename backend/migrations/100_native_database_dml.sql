ALTER TABLE database_assets ADD COLUMN native_write_credential_id uuid REFERENCES credentials(id);
ALTER TABLE database_assets ADD CONSTRAINT database_native_write_separate CHECK
 (native_write_credential_id IS NULL OR (engine='mysql' AND native_write_credential_id<>readonly_credential_id AND native_write_credential_id IS DISTINCT FROM write_credential_id));
ALTER TABLE database_sessions ADD COLUMN access_profile text NOT NULL DEFAULT 'readonly' CHECK(access_profile IN ('readonly','dml'));
INSERT INTO permissions VALUES ('database.write','database-asset','数据库原生受控 DML 写入');
INSERT INTO settings(key,typed_value) VALUES ('db-proxy-block-dml','true');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||'{"db-proxy-block-dml":true}'::jsonb,ARRAY['db-proxy-block-dml']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1),enforced=enforced||'{"db-proxy-block-dml":true}'::jsonb WHERE singleton=1;
UPDATE instance_state SET schema_version=100 WHERE singleton=1;
