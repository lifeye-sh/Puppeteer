CREATE TABLE gateway_groups(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),name text NOT NULL,enabled boolean NOT NULL DEFAULT true,version bigint NOT NULL DEFAULT 1,members jsonb NOT NULL);
ALTER TABLE assets ADD COLUMN gateway_group_id uuid REFERENCES gateway_groups(id);
ALTER TABLE assets ADD CONSTRAINT asset_group_route CHECK(gateway_group_id IS NULL OR (agent_id IS NULL AND jump_id IS NULL));
UPDATE instance_state SET schema_version=13;
