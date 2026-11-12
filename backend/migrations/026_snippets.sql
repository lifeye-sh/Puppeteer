CREATE TABLE snippets (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 owner_id uuid REFERENCES users(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 120),
 description text NOT NULL DEFAULT '',
 content text NOT NULL CHECK(octet_length(content) BETWEEN 1 AND 4096),
 version bigint NOT NULL DEFAULT 1,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX snippets_owner ON snippets(owner_id,updated_at DESC,id);
INSERT INTO permissions(action,menu_key,label) VALUES
 ('snippet.manage','snippet','管理系统命令片段');
UPDATE instance_state SET schema_version=26;
