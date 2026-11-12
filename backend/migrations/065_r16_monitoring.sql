CREATE TABLE monitor_rules (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL,
 metric text NOT NULL CHECK(metric IN ('agent.cpu','agent.memory','agent.disk','agent.silence','configuration.delay')),
 agent_id uuid REFERENCES agent_gateway_configs(id), threshold double precision NOT NULL,
 recovery double precision NOT NULL, hold_seconds integer NOT NULL CHECK(hold_seconds BETWEEN 0 AND 3600),
 enabled boolean NOT NULL, version bigint NOT NULL DEFAULT 1, created_by uuid NOT NULL REFERENCES users(id),
 pending_at timestamptz, firing boolean NOT NULL DEFAULT false, last_value double precision,
 sample_state text NOT NULL DEFAULT 'unknown', checked_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz
);
CREATE TABLE system_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), rule_id uuid REFERENCES monitor_rules(id),
 rule_name text NOT NULL, metric text NOT NULL, kind text NOT NULL CHECK(kind IN ('firing','recovered','cancelled')),
 value double precision, created_at timestamptz NOT NULL DEFAULT now(),
 acknowledged_at timestamptz, acknowledged_by uuid REFERENCES users(id)
);
CREATE INDEX system_events_created ON system_events(created_at DESC,id);
ALTER TABLE notification_events ADD COLUMN system_event_id uuid REFERENCES system_events(id);
UPDATE instance_state SET schema_version=65 WHERE singleton=1;
