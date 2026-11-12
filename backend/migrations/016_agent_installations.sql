CREATE TABLE agent_installations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 asset_id uuid NOT NULL REFERENCES assets(id),
 actor_id uuid NOT NULL REFERENCES users(id),
 agent_id uuid REFERENCES agent_gateway_configs(id),
 state text NOT NULL DEFAULT 'running' CHECK(state IN ('running','succeeded','failed')),
 step text NOT NULL DEFAULT 'queued',
 message text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX agent_installations_active_asset ON agent_installations(asset_id) WHERE state='running';
UPDATE instance_state SET schema_version=16;
