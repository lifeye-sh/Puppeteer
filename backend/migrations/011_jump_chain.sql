ALTER TABLE ssh_jump_hosts ADD COLUMN parent_id uuid REFERENCES ssh_jump_hosts(id);
ALTER TABLE ssh_jump_hosts ADD CONSTRAINT jump_not_self CHECK(parent_id <> id);
UPDATE instance_state SET schema_version=11;
