-- Backup catalog is distinct from restore state, which lives outside this DB.
CREATE TABLE backup_config (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 recipient text NOT NULL DEFAULT '',
 retention_days integer NOT NULL DEFAULT 31 CHECK(retention_days BETWEEN 1 AND 3650),
 schedule_enabled boolean NOT NULL DEFAULT false,
 schedule_time text NOT NULL DEFAULT '02:00',
 timezone text NOT NULL DEFAULT 'Asia/Shanghai',
 scope text NOT NULL DEFAULT 'control-plane' CHECK(scope IN ('control-plane','full')),
 revision bigint NOT NULL DEFAULT 1
);
INSERT INTO backup_config(singleton) VALUES(true);
CREATE TABLE backup_tasks (
 id uuid PRIMARY KEY REFERENCES jobs(id),
 actor_id uuid REFERENCES users(id),
 request_key uuid NOT NULL,
 config_snapshot jsonb NOT NULL,
 stage text NOT NULL DEFAULT 'queued',
 remark text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE NULLS NOT DISTINCT(actor_id,request_key)
);
CREATE TABLE backup_files (
 id uuid PRIMARY KEY REFERENCES backup_tasks(id),
 storage_key text NOT NULL UNIQUE,
 size bigint NOT NULL CHECK(size>0),
 sha256 text NOT NULL,
 manifest jsonb NOT NULL,
 state text NOT NULL DEFAULT 'available' CHECK(state IN ('available','deleting','missing')),
 created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO permissions(action,menu_key,label) VALUES('backup.manage','backup','备份管理'),('backup.export','backup','加密备份导出');
CREATE TABLE backup_export_tickets (
 token_hash text PRIMARY KEY, session_hash text NOT NULL, file_id uuid NOT NULL REFERENCES backup_files(id),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '1 minute', used_at timestamptz
);
CREATE TABLE backup_destinations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('s3','sftp','webdav')),enabled boolean NOT NULL DEFAULT false,
 config jsonb NOT NULL,secret bytea NOT NULL,revision bigint NOT NULL DEFAULT 1
);
CREATE TABLE backup_copies (
 task_id uuid NOT NULL REFERENCES backup_tasks(id),destination_id uuid NOT NULL REFERENCES backup_destinations(id),
 config_snapshot jsonb NOT NULL,secret bytea NOT NULL,remote_key text NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','copying','verified','failed','deleting','deleted')),
 attempts integer NOT NULL DEFAULT 0,verified_at timestamptz,reason text NOT NULL DEFAULT '',
 PRIMARY KEY(task_id,destination_id)
);
ALTER TABLE storage_nodes ADD COLUMN payload_unavailable boolean NOT NULL DEFAULT false;
CREATE TABLE restore_quarantine(id text PRIMARY KEY,completed_at timestamptz NOT NULL DEFAULT now());
UPDATE instance_state SET schema_version=30;
CREATE TABLE backup_recipient_proofs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),session_hash text NOT NULL UNIQUE,
 recipient text NOT NULL,response_hash text NOT NULL,
 expires_at timestamptz NOT NULL DEFAULT now()+interval '30 minutes'
);
