CREATE TABLE storage_spaces (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), owner_id uuid NOT NULL REFERENCES users(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100), shared boolean NOT NULL DEFAULT false,
 enabled boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO storage_spaces(id,owner_id,name) SELECT user_id,user_id,'个人存储' FROM user_storage;
ALTER TABLE user_storage DROP CONSTRAINT user_storage_user_id_fkey;
ALTER TABLE user_storage ADD FOREIGN KEY(user_id) REFERENCES storage_spaces(id);
CREATE TABLE storage_acl (
 space_id uuid NOT NULL REFERENCES storage_spaces(id), user_id uuid NOT NULL REFERENCES users(id),
 writable boolean NOT NULL DEFAULT false, PRIMARY KEY(space_id,user_id)
);
CREATE TABLE storage_preferences(user_id uuid PRIMARY KEY REFERENCES users(id),space_id uuid NOT NULL REFERENCES storage_spaces(id));
CREATE TABLE user_channel_policy (
 user_id uuid PRIMARY KEY REFERENCES users(id), upload boolean NOT NULL DEFAULT true, download boolean NOT NULL DEFAULT true,
 clipboard_copy boolean NOT NULL DEFAULT true, clipboard_paste boolean NOT NULL DEFAULT true
);
CREATE TABLE session_storage(session_id uuid PRIMARY KEY REFERENCES sessions(id),space_id uuid NOT NULL REFERENCES storage_spaces(id));
CREATE TABLE storage_file_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, actor_id uuid NOT NULL REFERENCES users(id),
 space_id uuid NOT NULL REFERENCES storage_spaces(id), node_id bigint NOT NULL, operation text NOT NULL,
 channel text NOT NULL, session_id text NOT NULL DEFAULT '', bytes bigint NOT NULL DEFAULT 0, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX storage_events_actor ON storage_file_events(actor_id,id DESC);
UPDATE instance_state SET schema_version=21;
