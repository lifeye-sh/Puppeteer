ALTER TABLE agent_gateway_configs ADD COLUMN last_connected_at timestamptz;
ALTER TABLE agent_gateway_configs ADD COLUMN last_heartbeat_at timestamptz;
UPDATE instance_state SET schema_version=8;
