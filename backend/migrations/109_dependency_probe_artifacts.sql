-- Only explicit backup verification creates external objects. Persist their
-- encrypted destination snapshot before upload so a restart can clean them up.
CREATE TABLE dependency_probe_artifacts (
 task_id text PRIMARY KEY REFERENCES configuration_checks(id),
 resource_id uuid NOT NULL UNIQUE,
 sealed bytea NOT NULL,
 remote_key text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
UPDATE instance_state SET schema_version=109 WHERE singleton=1;
