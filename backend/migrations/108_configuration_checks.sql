-- Evidence stores no configuration snapshots, credentials or raw probe errors.
CREATE TABLE configuration_checks (
 id text PRIMARY KEY,
 feature text NOT NULL,
 node_id text NOT NULL,
 revision bigint NOT NULL,
 actor_id text NOT NULL,
 state text NOT NULL CHECK (state IN ('running','succeeded','failed','cancelled','stale')),
 deadline timestamptz NOT NULL,
 started_at timestamptz NOT NULL DEFAULT now(),
 payload jsonb NOT NULL
);
CREATE INDEX configuration_checks_latest ON configuration_checks(feature, started_at DESC);
UPDATE instance_state SET schema_version=108 WHERE singleton=1;
