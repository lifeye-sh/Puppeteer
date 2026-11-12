CREATE TABLE approval_scopes (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),name text NOT NULL,
 requester_ids uuid[] NOT NULL DEFAULT '{}',department_ids uuid[] NOT NULL DEFAULT '{}',resource_ids uuid[] NOT NULL DEFAULT '{}',reviewer_ids uuid[] NOT NULL DEFAULT '{}',
 max_minutes integer NOT NULL CHECK(max_minutes BETWEEN 1 AND 10080),enabled boolean NOT NULL DEFAULT true,version bigint NOT NULL DEFAULT 1
);
CREATE TABLE access_requests (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),actor_id uuid NOT NULL REFERENCES users(id),resource_id uuid NOT NULL REFERENCES resource_registry(id),scope_id uuid NOT NULL REFERENCES approval_scopes(id),
 reason text NOT NULL,duration_minutes integer NOT NULL CHECK(duration_minutes BETWEEN 1 AND 10080),requested_start timestamptz NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected','cancelled','expired')),
 reviewer_id uuid REFERENCES users(id),decision_reason text NOT NULL DEFAULT '',grant_id uuid REFERENCES grants(id),starts_at timestamptz,expires_at timestamptz,
 version bigint NOT NULL DEFAULT 1,created_at timestamptz NOT NULL DEFAULT now(),decided_at timestamptz
);
CREATE UNIQUE INDEX access_request_pending ON access_requests(actor_id,resource_id) WHERE status='pending';
ALTER TABLE grants ADD COLUMN approval_id uuid REFERENCES access_requests(id);
CREATE UNIQUE INDEX grant_approval_once ON grants(approval_id) WHERE approval_id IS NOT NULL;
CREATE TABLE notification_channels(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),name text NOT NULL,type text NOT NULL CHECK(type IN ('smtp','wecom')),enabled boolean NOT NULL DEFAULT false,config jsonb NOT NULL,secret bytea,version bigint NOT NULL DEFAULT 1);
CREATE TABLE notification_bindings(user_id uuid NOT NULL REFERENCES users(id),channel_id uuid NOT NULL REFERENCES notification_channels(id),address text NOT NULL,enabled boolean NOT NULL DEFAULT true,PRIMARY KEY(user_id,channel_id));
CREATE TABLE notification_preferences(user_id uuid PRIMARY KEY REFERENCES users(id),enabled boolean NOT NULL DEFAULT true,quiet_start text NOT NULL DEFAULT '',quiet_end text NOT NULL DEFAULT '',timezone text NOT NULL DEFAULT 'Asia/Shanghai');
CREATE TABLE notification_rules(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),name text NOT NULL,enabled boolean NOT NULL DEFAULT true,events text[] NOT NULL,channel_ids uuid[] NOT NULL,recipient_mode text NOT NULL CHECK(recipient_mode IN ('requester','reviewers','specified')),recipient_ids uuid[] NOT NULL DEFAULT '{}',version bigint NOT NULL DEFAULT 1);
CREATE TABLE notification_events(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),event_key text NOT NULL UNIQUE,event_type text NOT NULL,request_id uuid REFERENCES access_requests(id),actor_id uuid NOT NULL REFERENCES users(id),reviewer_ids uuid[] NOT NULL DEFAULT '{}',payload jsonb NOT NULL,expanded boolean NOT NULL DEFAULT false,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE notification_deliveries(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),event_id uuid REFERENCES notification_events(id),rule_id uuid REFERENCES notification_rules(id),channel_id uuid NOT NULL REFERENCES notification_channels(id),recipient_id uuid NOT NULL REFERENCES users(id),test boolean NOT NULL DEFAULT false,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sending','delivered','failed','skipped')),attempts integer NOT NULL DEFAULT 0,next_attempt timestamptz NOT NULL DEFAULT now(),lease_until timestamptz,lease_token text NOT NULL DEFAULT '',last_error text NOT NULL DEFAULT '',sent_at timestamptz,created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(event_id,rule_id,channel_id,recipient_id));
UPDATE instance_state SET schema_version=24;
