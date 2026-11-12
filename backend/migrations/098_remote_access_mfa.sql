CREATE TABLE session_access_proofs (
 user_id uuid NOT NULL REFERENCES users(id),
 auth_hash text NOT NULL CHECK(auth_hash ~ '^[a-f0-9]{64}$'),
 factor_hash text NOT NULL,
 user_version bigint NOT NULL,
 auth_epoch bigint NOT NULL,
 verified_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 uses integer NOT NULL DEFAULT 0 CHECK(uses>=0),
 PRIMARY KEY(user_id,auth_hash)
);
INSERT INTO settings(key,typed_value) VALUES ('access-require-mfa','false'),('access-mfa-expires-at','5');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||'{"access-require-mfa":false,"access-mfa-expires-at":5}'::jsonb,ARRAY['access-require-mfa','access-mfa-expires-at']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1),enforced=enforced||'{"access-require-mfa":false,"access-mfa-expires-at":5}'::jsonb WHERE singleton=1;
UPDATE instance_state SET schema_version=98 WHERE singleton=1;
