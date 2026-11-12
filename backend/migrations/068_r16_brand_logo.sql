CREATE TABLE brand_images (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 sha256 text NOT NULL UNIQUE CHECK(length(sha256)=64),
 data bytea NOT NULL CHECK(octet_length(data) BETWEEN 1 AND 2097152),
 created_by uuid NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO settings(key,typed_value) VALUES('system-logo','""');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot || '{"system-logo":""}'::jsonb,ARRAY['system-logo']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
)
UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE instance_state SET schema_version=68 WHERE singleton=1;
