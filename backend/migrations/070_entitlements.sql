CREATE TABLE entitlement_licenses (
 revision bigserial PRIMARY KEY,
 instance_id uuid NOT NULL,
 license_id uuid NOT NULL,
 token text NOT NULL CHECK(octet_length(token) BETWEEN 1 AND 32768),
 digest text NOT NULL CHECK(length(digest)=64),
 actor_id uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE entitlement_head (singleton integer PRIMARY KEY CHECK(singleton=1),revision bigint REFERENCES entitlement_licenses(revision));
INSERT INTO entitlement_head(singleton) VALUES(1);
ALTER TABLE sessions ADD COLUMN quota_epoch bigint NOT NULL DEFAULT 1;
ALTER TABLE database_sessions ADD COLUMN quota_epoch bigint NOT NULL DEFAULT 1;
ALTER TABLE database_sessions ADD COLUMN quota_lease_until timestamptz NOT NULL DEFAULT now();
UPDATE sessions SET quota_epoch=(SELECT auth_epoch FROM instance_state WHERE singleton=1);
UPDATE database_sessions SET quota_epoch=(SELECT auth_epoch FROM instance_state WHERE singleton=1);
CREATE INDEX database_session_quota ON database_sessions(quota_lease_until) WHERE ended_at IS NULL;
UPDATE instance_state SET schema_version=70 WHERE singleton=1;
