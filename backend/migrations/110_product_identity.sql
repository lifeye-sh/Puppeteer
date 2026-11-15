-- Discover the previous generated device label from its database default.
-- Replace only generated names; retain user-chosen names and audit history.
DO $$
DECLARE default_expression text; previous_label text; previous_brand text;
BEGIN
 SELECT pg_get_expr(d.adbin,d.adrelid) INTO default_expression
 FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum
 WHERE d.adrelid='native_client_devices'::regclass AND a.attname='name';
 IF default_expression IS NOT NULL THEN
  EXECUTE 'SELECT '||default_expression INTO previous_label;
  previous_brand := regexp_replace(previous_label,' 客户端$','');
  UPDATE native_client_devices SET name='Puppeteer 客户端' WHERE name=previous_label;
  UPDATE oauth_clients SET name='Puppeteer 原生客户端'
   WHERE id='00000000-0000-4000-8000-000000000014' AND name=previous_brand||' 原生客户端';
  UPDATE settings SET typed_value='"Puppeteer"'::jsonb,revision=revision+1
   WHERE key='system-name' AND typed_value #>> '{}' IN (previous_brand,previous_brand||' · 新后端');
  WITH added AS (
   INSERT INTO configuration_revisions(snapshot,changed_keys)
   SELECT r.snapshot||'{"system-name":"Puppeteer"}'::jsonb,ARRAY['system-name']
   FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id
   WHERE r.snapshot->>'system-name' IN (previous_brand,previous_brand||' · 新后端')
    AND r.snapshot->>'system-name'<>'Puppeteer'
   RETURNING id
  ) UPDATE configuration_head SET revision=coalesce((SELECT id FROM added),revision) WHERE singleton=1;
 END IF;
END $$;
ALTER TABLE native_client_devices ALTER COLUMN name SET DEFAULT 'Puppeteer 客户端';
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1) WHERE singleton=1;
UPDATE instance_state SET schema_version=110 WHERE singleton=1;
