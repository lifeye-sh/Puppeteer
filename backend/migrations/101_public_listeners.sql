INSERT INTO settings(key,typed_value) VALUES
 ('http-server-addr','""'),('website-server-enabled','true'),('website-server-addr','""'),
 ('native-rdp-enabled','true'),('native-rdp-addr','""'),('native-rdp-public-address','""');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||'{"http-server-addr":"","website-server-enabled":true,"website-server-addr":"","native-rdp-enabled":true,"native-rdp-addr":"","native-rdp-public-address":""}'::jsonb,
 ARRAY['http-server-addr','website-server-enabled','website-server-addr','native-rdp-enabled','native-rdp-addr','native-rdp-public-address']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1) WHERE singleton=1;
UPDATE instance_state SET schema_version=101 WHERE singleton=1;
