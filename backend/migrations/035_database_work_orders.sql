CREATE TABLE database_work_orders (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),asset_id uuid NOT NULL REFERENCES database_assets(id),requester_id uuid NOT NULL REFERENCES users(id),reviewer_id uuid REFERENCES users(id),
 title text NOT NULL,sql_text text NOT NULL CHECK(octet_length(sql_text)<=1048576),sql_sha256 text NOT NULL,
 mode text NOT NULL CHECK(mode IN ('transaction','ddl')),asset_version bigint NOT NULL,credential_version integer NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','approved','running','succeeded','failed','partial','cancelled','rejected','unknown')),
 version bigint NOT NULL DEFAULT 1,run_id uuid,cancel_requested boolean NOT NULL DEFAULT false,
 reason text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),reviewed_at timestamptz,started_at timestamptz,finished_at timestamptz,heartbeat_at timestamptz
);
CREATE INDEX database_work_orders_pending ON database_work_orders(created_at) WHERE state IN ('pending','approved','running');
CREATE TABLE database_work_order_results (
 order_id uuid NOT NULL REFERENCES database_work_orders(id),statement_index integer NOT NULL,
 state text NOT NULL,affected_rows bigint NOT NULL DEFAULT 0,result jsonb NOT NULL DEFAULT '{}',duration_ms bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(order_id,statement_index)
);
ALTER TABLE notification_events ADD COLUMN database_order_id uuid REFERENCES database_work_orders(id);
ALTER TABLE database_sql_logs ADD COLUMN work_order_id uuid REFERENCES database_work_orders(id);
ALTER TABLE database_sql_logs ADD CHECK ((session_id IS NOT NULL)::integer+(work_order_id IS NOT NULL)::integer=1);
UPDATE instance_state SET schema_version=35 WHERE singleton=1;
