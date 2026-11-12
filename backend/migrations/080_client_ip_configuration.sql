INSERT INTO settings(key,typed_value)
 SELECT 'ip-extractor',to_jsonb(CASE WHEN cardinality(api_trusted_proxies)>0 THEN 'x-forwarded-for' ELSE 'direct' END) FROM website_settings
 UNION ALL SELECT 'ip-trust-list',to_jsonb(coalesce(array_to_string(api_trusted_proxies,','),'')) FROM website_settings;
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||(SELECT jsonb_object_agg(key,typed_value) FROM settings WHERE key IN ('ip-extractor','ip-trust-list')),ARRAY['ip-extractor','ip-trust-list'] FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1),enforced=enforced||(SELECT jsonb_object_agg(key,typed_value) FROM settings WHERE key IN ('ip-extractor','ip-trust-list')) WHERE singleton=1;
UPDATE instance_state SET schema_version=80 WHERE singleton=1;
