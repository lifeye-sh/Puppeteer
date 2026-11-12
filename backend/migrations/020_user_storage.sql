CREATE TABLE user_storage (
 user_id uuid PRIMARY KEY REFERENCES users(id),
 quota_bytes bigint NOT NULL DEFAULT 5368709120 CHECK(quota_bytes BETWEEN 0 AND 1099511627776),
 used_bytes bigint NOT NULL DEFAULT 0 CHECK(used_bytes>=0),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE storage_nodes (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES user_storage(user_id),
 parent_id bigint REFERENCES storage_nodes(id),
 name text NOT NULL, directory boolean NOT NULL DEFAULT false,
 size bigint NOT NULL DEFAULT 0 CHECK(size>=0),
 modified_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE NULLS NOT DISTINCT(user_id,parent_id,name)
);
CREATE INDEX storage_nodes_owner_parent ON storage_nodes(user_id,parent_id);
CREATE TABLE storage_chunks (
 node_id bigint NOT NULL REFERENCES storage_nodes(id) ON DELETE CASCADE,
 chunk_index bigint NOT NULL CHECK(chunk_index>=0),
 data bytea NOT NULL CHECK(octet_length(data)<=65536),
 PRIMARY KEY(node_id,chunk_index)
);
UPDATE instance_state SET schema_version=20;
