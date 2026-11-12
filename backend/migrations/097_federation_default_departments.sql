CREATE TABLE federation_default_departments (
 kind text PRIMARY KEY CHECK(kind IN ('oidc','wecom')),
 department_id uuid NOT NULL REFERENCES departments(id) ON DELETE RESTRICT
);
INSERT INTO settings(key,typed_value) VALUES('oidc-department','""'),('wechat-work-department','""');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||'{"oidc-department":"","wechat-work-department":""}'::jsonb,ARRAY['oidc-department','wechat-work-department']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1) WHERE singleton=1;
UPDATE instance_state SET schema_version=97 WHERE singleton=1;
