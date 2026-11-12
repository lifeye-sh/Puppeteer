INSERT INTO settings(key,typed_value) VALUES
 ('ssh-server-enabled','true'),('ssh-server-addr','""'),
 ('db-proxy-enabled','true'),('db-proxy-addr','""'),
 ('postgres-proxy-enabled','true'),('postgres-proxy-addr','""'),
 ('db-proxy-certificate-id','""');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot || '{"ssh-server-enabled":true,"ssh-server-addr":"","db-proxy-enabled":true,"db-proxy-addr":"","postgres-proxy-enabled":true,"postgres-proxy-addr":"","db-proxy-certificate-id":""}'::jsonb,
 ARRAY['ssh-server-enabled','ssh-server-addr','db-proxy-enabled','db-proxy-addr','postgres-proxy-enabled','postgres-proxy-addr','db-proxy-certificate-id']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
)
UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE instance_state SET schema_version=77 WHERE singleton=1;
