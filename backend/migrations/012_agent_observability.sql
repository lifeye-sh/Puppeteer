ALTER TABLE agent_gateway_configs ADD COLUMN metrics jsonb;
ALTER TABLE agent_gateway_configs ADD COLUMN metrics_at timestamptz;
ALTER TABLE agent_gateway_configs ADD COLUMN previous_credential_hash text;
ALTER TABLE agent_gateway_configs ADD COLUMN previous_expires_at timestamptz;
ALTER TABLE agent_gateway_configs ADD COLUMN rotation_requested boolean NOT NULL DEFAULT false;
UPDATE instance_state SET schema_version=12;
