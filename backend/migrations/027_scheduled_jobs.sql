-- F00 durable execution leases. A dispatched job is never automatically replayed.
CREATE TABLE jobs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 kind text NOT NULL,
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','succeeded','failed','partial','cancelled','unknown','skipped')),
 lease_token uuid, lease_until timestamptz,
 cancel_requested boolean NOT NULL DEFAULT false,
 reason text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), started_at timestamptz, ended_at timestamptz
);
CREATE INDEX jobs_ready ON jobs(created_at,id) WHERE state='queued';
CREATE INDEX jobs_expiry ON jobs(lease_until) WHERE state='running';
-- M12 definitions and immutable execution snapshots.
CREATE TABLE schedules (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), owner_id uuid NOT NULL REFERENCES users(id),
 name text NOT NULL, kind text NOT NULL CHECK(kind='ssh.command'),
 command text NOT NULL, asset_ids uuid[] NOT NULL,
 cron text NOT NULL DEFAULT '', timezone text NOT NULL DEFAULT 'Asia/Shanghai',
 enabled boolean NOT NULL DEFAULT false, misfire text NOT NULL DEFAULT 'skip' CHECK(misfire IN ('skip','fire_once')),
 timeout_seconds integer NOT NULL CHECK(timeout_seconds BETWEEN 1 AND 600),
 source_ip text NOT NULL, next_run timestamptz, version bigint NOT NULL DEFAULT 1,
 pause_reason text NOT NULL DEFAULT '', deleted_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX schedules_due ON schedules(next_run) WHERE enabled AND deleted_at IS NULL;
CREATE TABLE scheduled_runs (
 id uuid PRIMARY KEY REFERENCES jobs(id), schedule_id uuid NOT NULL REFERENCES schedules(id),
 actor_id uuid NOT NULL REFERENCES users(id), trigger_key text NOT NULL,
 snapshot jsonb NOT NULL, scheduled_at timestamptz,
 UNIQUE(schedule_id,trigger_key)
);
CREATE INDEX scheduled_runs_history ON scheduled_runs(schedule_id,id);
CREATE TABLE scheduled_run_targets (
 run_id uuid NOT NULL REFERENCES scheduled_runs(id), asset_id uuid NOT NULL REFERENCES resource_registry(id),
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','succeeded','failed','cancelled','unknown')),
 reason text NOT NULL DEFAULT '', output text NOT NULL DEFAULT '', truncated boolean NOT NULL DEFAULT false,
 exit_code integer, started_at timestamptz, ended_at timestamptz,
 PRIMARY KEY(run_id,asset_id)
);
INSERT INTO permissions(action,menu_key,label) VALUES('task.manage','scheduled-task','任务调度');
UPDATE instance_state SET schema_version=27;
