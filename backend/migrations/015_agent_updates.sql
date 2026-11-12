ALTER TABLE agent_gateway_configs ADD COLUMN update_request text NOT NULL DEFAULT '' CHECK(update_request IN ('','check','install'));
ALTER TABLE agent_gateway_configs ADD COLUMN capabilities jsonb NOT NULL DEFAULT '[]'::jsonb;
UPDATE instance_state SET schema_version=15;
