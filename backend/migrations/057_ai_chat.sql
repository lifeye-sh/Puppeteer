-- R15.1: bounded text chat only. Tool execution remains unavailable.
CREATE TABLE ai_settings (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 enabled boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1
);
INSERT INTO ai_settings(singleton) VALUES(true);
CREATE TABLE ai_profiles (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 name text NOT NULL,
 base_url text NOT NULL,
 model text NOT NULL,
 secret bytea NOT NULL,
 allow_private boolean NOT NULL DEFAULT false,
 enabled boolean NOT NULL DEFAULT false,
 max_output_tokens integer NOT NULL CHECK(max_output_tokens BETWEEN 64 AND 4096),
 version bigint NOT NULL DEFAULT 1,
 deleted_at timestamptz
);
CREATE TABLE ai_conversations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id uuid NOT NULL REFERENCES users(id),
 profile_id uuid NOT NULL REFERENCES ai_profiles(id),
 title text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ai_conversations_owner ON ai_conversations(user_id, updated_at DESC);
CREATE TABLE ai_turns (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 conversation_id uuid NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES users(id),
 client_message_id uuid NOT NULL,
 prompt text NOT NULL CHECK(octet_length(prompt) BETWEEN 1 AND 8192),
 output text NOT NULL DEFAULT '' CHECK(octet_length(output)<=131072),
 state text NOT NULL CHECK(state IN ('running','succeeded','failed','interrupted')),
 reason text NOT NULL DEFAULT '',
 model text NOT NULL,
 prompt_tokens integer NOT NULL DEFAULT 0 CHECK(prompt_tokens>=0),
 completion_tokens integer NOT NULL DEFAULT 0 CHECK(completion_tokens>=0),
 created_at timestamptz NOT NULL DEFAULT now(),
 deadline timestamptz NOT NULL DEFAULT now()+interval '100 seconds',
 UNIQUE(conversation_id, client_message_id)
);
CREATE UNIQUE INDEX ai_one_active_turn_per_user ON ai_turns(user_id) WHERE state='running';
-- Independent of conversation deletion so deleting history cannot reset quotas.
CREATE TABLE ai_usage_ledger (
 turn_id uuid PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 prompt_tokens integer NOT NULL DEFAULT 0,
 completion_tokens integer NOT NULL DEFAULT 0
);
CREATE INDEX ai_usage_user_time ON ai_usage_ledger(user_id,created_at DESC);
UPDATE instance_state SET schema_version=57 WHERE singleton=1;
