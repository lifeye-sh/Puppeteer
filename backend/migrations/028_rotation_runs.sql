-- Durable rotation intent. An uncertain target retains its account lock until
-- evidence resolves it; restarting a worker never repeats a remote mutation.
CREATE TABLE rotation_runs (
 id uuid PRIMARY KEY REFERENCES jobs(id),
 actor_id uuid NOT NULL REFERENCES users(id),
 request_key uuid NOT NULL,
 source_ip text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(actor_id,request_key)
);
CREATE TABLE rotation_targets (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 run_id uuid NOT NULL REFERENCES rotation_runs(id),
 asset_id uuid NOT NULL REFERENCES resource_registry(id),
 asset_version bigint NOT NULL,
 credential_id uuid NOT NULL REFERENCES credentials(id),
 credential_version bigint NOT NULL,
 account_key text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('password','private-key')),
 candidate bytea NOT NULL,
 public_key text NOT NULL DEFAULT '',
 new_credential_id uuid REFERENCES credentials(id),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','applying','verifying','succeeded','failed','unknown','cancelled')),
 reason text NOT NULL DEFAULT '',
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(run_id,asset_id)
);
CREATE UNIQUE INDEX rotation_account_lock ON rotation_targets(account_key)
 WHERE state IN ('pending','applying','verifying','unknown');
CREATE INDEX rotation_target_run ON rotation_targets(run_id,id);
ALTER TABLE schedules DROP CONSTRAINT schedules_kind_check;
ALTER TABLE schedules ADD CONSTRAINT schedules_kind_check CHECK(kind IN ('ssh.command','credential.rotate'));
INSERT INTO permissions(action,menu_key,label) VALUES('credential.rotate','credential-rotation','凭据轮换');
UPDATE instance_state SET schema_version=28;
