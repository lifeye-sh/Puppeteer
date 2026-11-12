ALTER TABLE assets ADD COLUMN agent_id uuid REFERENCES agent_gateway_configs(id);
UPDATE instance_state SET schema_version=9;
