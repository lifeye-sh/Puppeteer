INSERT INTO settings(key,typed_value) VALUES('agent-gateway-endpoint','""');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||jsonb_build_object('agent-gateway-endpoint',''),ARRAY['agent-gateway-endpoint'] FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1) WHERE singleton=1;
UPDATE instance_state SET schema_version=85 WHERE singleton=1;
