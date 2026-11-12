INSERT INTO settings(key,typed_value)
 SELECT 'ssh-server-port-forwarding-enabled',to_jsonb(enabled) FROM native_forward_settings WHERE singleton=1
 UNION ALL
 SELECT 'ssh-server-port-forwarding-host-port',to_jsonb((SELECT coalesce(jsonb_agg(jsonb_build_object('assetId',r->>'assetId','host',r->>'host','port',(r->>'port')::int)),'[]'::jsonb)::text FROM jsonb_array_elements(rules) r)) FROM native_forward_settings WHERE singleton=1;
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||(SELECT jsonb_object_agg(key,typed_value) FROM settings WHERE key IN ('ssh-server-port-forwarding-enabled','ssh-server-port-forwarding-host-port')),
 ARRAY['ssh-server-port-forwarding-enabled','ssh-server-port-forwarding-host-port']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1),enforced=enforced||(SELECT jsonb_object_agg(key,typed_value) FROM settings WHERE key IN ('ssh-server-port-forwarding-enabled','ssh-server-port-forwarding-host-port')) WHERE singleton=1;
UPDATE instance_state SET schema_version=92 WHERE singleton=1;
