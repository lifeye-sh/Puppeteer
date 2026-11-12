CREATE TABLE credentials (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL, kind text NOT NULL CHECK(kind IN ('password','private-key')),
 username text NOT NULL, active_version bigint NOT NULL DEFAULT 0, version bigint NOT NULL DEFAULT 1,
 public_key text NOT NULL DEFAULT '', deleted_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX credentials_name ON credentials(name) WHERE deleted_at IS NULL;
CREATE TABLE secret_versions (
 credential_id uuid NOT NULL REFERENCES credentials(id), version bigint NOT NULL, ciphertext bytea NOT NULL,
 wrapped_dek bytea NOT NULL, kek_id text NOT NULL DEFAULT 'local-v1', algorithm text NOT NULL DEFAULT 'AES-256-GCM',
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(credential_id,version)
);
CREATE TABLE secret_access_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), actor_id uuid NOT NULL REFERENCES users(id), credential_id uuid NOT NULL,
 version bigint NOT NULL, purpose text NOT NULL, request_id text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(credential_id,version) REFERENCES secret_versions(credential_id,version)
);
CREATE TABLE resource_registry (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), kind text NOT NULL CHECK(kind IN ('asset','group')),
 display_name text NOT NULL, enabled boolean NOT NULL DEFAULT true, version bigint NOT NULL DEFAULT 1,
 deleted_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(id,kind)
);
CREATE UNIQUE INDEX resource_registry_names ON resource_registry(kind,display_name) WHERE deleted_at IS NULL;
CREATE TABLE resource_groups (
 id uuid PRIMARY KEY, kind text NOT NULL DEFAULT 'group' CHECK(kind='group'), parent_id uuid REFERENCES resource_groups(id),
 FOREIGN KEY(id,kind) REFERENCES resource_registry(id,kind)
);
CREATE TABLE assets (
 id uuid PRIMARY KEY, kind text NOT NULL DEFAULT 'asset' CHECK(kind='asset'), protocol text NOT NULL CHECK(protocol='ssh'),
 alias text NOT NULL DEFAULT '', host text NOT NULL, port integer NOT NULL CHECK(port BETWEEN 1 AND 65535),
 group_id uuid REFERENCES resource_groups(id), credential_id uuid NOT NULL REFERENCES credentials(id),
 description text NOT NULL DEFAULT '', connect_timeout integer NOT NULL DEFAULT 10 CHECK(connect_timeout BETWEEN 1 AND 300),
 FOREIGN KEY(id,kind) REFERENCES resource_registry(id,kind)
);
CREATE UNIQUE INDEX assets_alias ON assets(alias) WHERE alias<>'';
CREATE INDEX assets_group ON assets(group_id);
CREATE INDEX assets_credential ON assets(credential_id);
CREATE TABLE resource_tags(resource_id uuid NOT NULL REFERENCES resource_registry(id), tag text NOT NULL CHECK(length(tag) BETWEEN 1 AND 40), PRIMARY KEY(resource_id,tag));
CREATE INDEX resource_tags_filter ON resource_tags(tag,resource_id);
CREATE TABLE grants (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid REFERENCES users(id), department_id uuid REFERENCES departments(id),
 resource_id uuid NOT NULL REFERENCES resource_registry(id), valid_from timestamptz NOT NULL DEFAULT now(), expires_at timestamptz,
 revoked_at timestamptz, version bigint NOT NULL DEFAULT 1, created_at timestamptz NOT NULL DEFAULT now(),
 CHECK(num_nonnulls(user_id,department_id)=1), CHECK(expires_at IS NULL OR expires_at>valid_from)
);
CREATE UNIQUE INDEX grants_active_user ON grants(user_id,resource_id) WHERE revoked_at IS NULL AND user_id IS NOT NULL;
CREATE UNIQUE INDEX grants_active_department ON grants(department_id,resource_id) WHERE revoked_at IS NULL AND department_id IS NOT NULL;
ALTER TABLE instance_state ADD COLUMN vault_key_fingerprint text NOT NULL DEFAULT '';
INSERT INTO permissions VALUES ('asset.read','asset','资产目录查看'),('credential.read','credential','凭据元数据查看'),('grant.read','authorised-asset','资源授权查看');
UPDATE instance_state SET schema_version=3;
