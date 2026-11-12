-- M07 owns mounts and bounded upload staging; M08 owns file operation evidence.
CREATE TABLE filesystem_mounts (
 session_id uuid PRIMARY KEY REFERENCES sessions(id), root text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE upload_tasks (
 id uuid PRIMARY KEY, session_id uuid NOT NULL REFERENCES sessions(id), actor_id uuid NOT NULL REFERENCES users(id),
 asset_id uuid NOT NULL REFERENCES resource_registry(id), root text NOT NULL, path text NOT NULL,
 size bigint NOT NULL CHECK(size BETWEEN 0 AND 16777216), received bigint NOT NULL DEFAULT 0,
 expected_hash text NOT NULL DEFAULT '', content_hash text NOT NULL DEFAULT '',
 state text NOT NULL DEFAULT 'receiving' CHECK(state IN ('receiving','transferring','committing','succeeded','failed','canceled','interrupted')),
 cancel_requested boolean NOT NULL DEFAULT false, cleanup_needed boolean NOT NULL DEFAULT false,
 message text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '15 minutes'
);
CREATE INDEX upload_tasks_actor ON upload_tasks(actor_id,created_at DESC);
CREATE TABLE upload_chunks (
 task_id uuid NOT NULL REFERENCES upload_tasks(id), offset_bytes bigint NOT NULL,
 payload bytea NOT NULL CHECK(octet_length(payload) BETWEEN 1 AND 1048576), PRIMARY KEY(task_id,offset_bytes)
);
CREATE TABLE filesystem_logs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), session_id uuid NOT NULL REFERENCES sessions(id), actor_id uuid NOT NULL REFERENCES users(id),
 action text NOT NULL, path text NOT NULL, destination text NOT NULL DEFAULT '', task_id uuid,
 state text NOT NULL DEFAULT 'running', bytes bigint NOT NULL DEFAULT 0, message text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), ended_at timestamptz
);
CREATE INDEX filesystem_logs_actor ON filesystem_logs(actor_id,created_at DESC);
CREATE UNIQUE INDEX filesystem_logs_task ON filesystem_logs(task_id) WHERE task_id IS NOT NULL;
UPDATE instance_state SET schema_version=6;
ALTER TABLE recordings ADD COLUMN sha256 text NOT NULL DEFAULT '';
ALTER TABLE recordings ADD COLUMN sealed_at timestamptz;
ALTER TABLE recordings ADD COLUMN integrity_state text NOT NULL DEFAULT 'unsealed';
