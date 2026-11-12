-- Rebrand the original defaults and this installation's former name.
-- Keep independently customized installations and all historical snapshots intact.
UPDATE settings SET typed_value='"Puppeteer"'::jsonb,revision=revision+1
WHERE key='system-name' AND typed_value #>> '{}' IN ('Next Terminal','Next Terminal · 新后端','CTerminal');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||'{"system-name":"Puppeteer"}'::jsonb,ARRAY['system-name']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id
 WHERE r.snapshot->>'system-name' IN ('Next Terminal','Next Terminal · 新后端','CTerminal')
 RETURNING id
)
UPDATE configuration_head SET revision=coalesce((SELECT id FROM added),revision) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1) WHERE singleton=1;
UPDATE instance_state SET schema_version=103 WHERE singleton=1;
