ALTER TABLE ai_settings ADD COLUMN config jsonb NOT NULL DEFAULT '{}';
ALTER TABLE ai_profiles ADD COLUMN config jsonb NOT NULL DEFAULT '{}';
ALTER TABLE ai_conversations ADD COLUMN parent_id uuid;
ALTER TABLE ai_conversations ADD COLUMN summary text NOT NULL DEFAULT '' CHECK(octet_length(summary)<=16384);
ALTER TABLE ai_conversations ADD COLUMN compacted_before timestamptz;
ALTER TABLE ai_turns DROP CONSTRAINT ai_turns_state_check;
ALTER TABLE ai_turns ADD CHECK(state IN ('queued','running','succeeded','failed','interrupted'));
DROP INDEX ai_one_active_turn_per_user;
CREATE UNIQUE INDEX ai_one_active_turn_per_user ON ai_turns(user_id) WHERE state IN ('queued','running');
ALTER TABLE ai_turns ADD COLUMN tool_calls jsonb NOT NULL DEFAULT '[]';
ALTER TABLE ai_usage_ledger ADD COLUMN profile_id uuid;
ALTER TABLE ai_usage_ledger ADD COLUMN model text NOT NULL DEFAULT '';
ALTER TABLE ai_usage_ledger ADD COLUMN reserved_micros bigint NOT NULL DEFAULT 0 CHECK(reserved_micros>=0);
ALTER TABLE ai_usage_ledger ADD COLUMN charged_micros bigint NOT NULL DEFAULT 0 CHECK(charged_micros>=0);
ALTER TABLE ai_usage_ledger ADD COLUMN input_rate bigint NOT NULL DEFAULT 0;
ALTER TABLE ai_usage_ledger ADD COLUMN output_rate bigint NOT NULL DEFAULT 0;
ALTER TABLE ai_usage_ledger ADD COLUMN usage_known boolean NOT NULL DEFAULT false;
CREATE TABLE ai_jobs (
 turn_id uuid PRIMARY KEY REFERENCES ai_turns(id) ON DELETE CASCADE,
 state text NOT NULL CHECK(state IN ('queued','running','finished','interrupted')),
 payload bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 started_at timestamptz,
 deadline timestamptz NOT NULL DEFAULT now()+interval '15 minutes'
);
CREATE INDEX ai_jobs_ready ON ai_jobs(created_at) WHERE state='queued';
CREATE TABLE ai_events (
 seq bigserial PRIMARY KEY,
 conversation_id uuid NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
 turn_id uuid,
 event_type text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ai_events_conversation ON ai_events(conversation_id,seq);
CREATE FUNCTION ai_turn_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 INSERT INTO ai_events(conversation_id,turn_id,event_type) VALUES(NEW.conversation_id,NEW.id,'turn'); RETURN NEW;
END $$;
CREATE TRIGGER ai_turn_events AFTER INSERT OR UPDATE ON ai_turns FOR EACH ROW EXECUTE FUNCTION ai_turn_event();
CREATE TABLE ai_tool_executions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 conversation_id uuid NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
 turn_id uuid REFERENCES ai_turns(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES users(id),
 client_id uuid NOT NULL,
 name text NOT NULL,
 arguments jsonb NOT NULL,
 parameter_hash text NOT NULL,
 asset_version bigint NOT NULL,
 credential_version bigint NOT NULL,
 state text NOT NULL CHECK(state IN ('awaiting_approval','approved','running','succeeded','failed','rejected','cancelled','timed_out','unknown','needs_revision')),
 output text NOT NULL DEFAULT '' CHECK(octet_length(output)<=262144),
 reason text NOT NULL DEFAULT '',
 cancel_requested boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL DEFAULT 1,
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '10 minutes',
 started_at timestamptz,
 finished_at timestamptz,
 UNIQUE(user_id,client_id)
);
CREATE TABLE ai_tool_approvals (
 execution_id uuid PRIMARY KEY REFERENCES ai_tool_executions(id) ON DELETE CASCADE,
 actor_id uuid NOT NULL REFERENCES users(id),
 parameter_hash text NOT NULL,
 decision text NOT NULL CHECK(decision IN ('approve','reject')),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE user_preferences (
 user_id uuid NOT NULL REFERENCES users(id),
 namespace text NOT NULL,
 value jsonb NOT NULL DEFAULT '{}',
 version bigint NOT NULL DEFAULT 1,
 PRIMARY KEY(user_id,namespace)
);
UPDATE instance_state SET schema_version=59 WHERE singleton=1;
