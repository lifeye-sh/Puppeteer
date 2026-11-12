CREATE TABLE storage_upload_tasks (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 actor_id uuid NOT NULL REFERENCES users(id),
 space_id uuid NOT NULL REFERENCES storage_spaces(id),
 parent_id bigint NOT NULL,
 name text NOT NULL,
 size bigint NOT NULL CHECK(size BETWEEN 0 AND 1073741824),
 received bigint NOT NULL DEFAULT 0 CHECK(received>=0 AND received<=size),
 state text NOT NULL DEFAULT 'uploading' CHECK(state IN ('uploading','complete','cancelled','expired')),
 node_id bigint,
 expires_at timestamptz NOT NULL DEFAULT now()+interval '24 hours',
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX storage_upload_active_name ON storage_upload_tasks(space_id,parent_id,name) WHERE state='uploading';
CREATE INDEX storage_upload_expiry ON storage_upload_tasks(expires_at) WHERE state='uploading';
CREATE TABLE storage_upload_parts (
 upload_id uuid NOT NULL REFERENCES storage_upload_tasks(id) ON DELETE CASCADE,
 chunk_index bigint NOT NULL,
 data bytea NOT NULL CHECK(octet_length(data)<=65536),
 PRIMARY KEY(upload_id,chunk_index)
);
ALTER TABLE storage_file_events ADD COLUMN node_name text NOT NULL DEFAULT '';
UPDATE instance_state SET schema_version=22;
