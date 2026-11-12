CREATE TABLE ssh_jump_hosts (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 name text NOT NULL,
 host text NOT NULL,
 port integer NOT NULL CHECK(port BETWEEN 1 AND 65535),
 credential_id uuid NOT NULL REFERENCES credentials(id),
 enabled boolean NOT NULL DEFAULT true,
 version bigint NOT NULL DEFAULT 1,
 public_key text NOT NULL DEFAULT '',
 fingerprint text NOT NULL DEFAULT ''
);
ALTER TABLE assets ADD COLUMN jump_id uuid REFERENCES ssh_jump_hosts(id);
ALTER TABLE assets ADD CONSTRAINT asset_single_route CHECK(agent_id IS NULL OR jump_id IS NULL);
UPDATE instance_state SET schema_version=10;
