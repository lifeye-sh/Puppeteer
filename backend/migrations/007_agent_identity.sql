-- M05: registration and device identity. Connectivity remains disabled until tunnel delivery.
CREATE TABLE agent_registration_tokens (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), token_hash text NOT NULL UNIQUE,
 name text NOT NULL, network_scope jsonb NOT NULL, created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL DEFAULT now()+interval '10 minutes',
 consumed_at timestamptz, revoked_at timestamptz
);
CREATE TABLE agent_gateway_configs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), registration_id uuid NOT NULL UNIQUE REFERENCES agent_registration_tokens(id),
 name text NOT NULL, credential_hash text NOT NULL UNIQUE, credential_version bigint NOT NULL DEFAULT 1,
 connection_epoch bigint NOT NULL DEFAULT 0, protocol_version text NOT NULL DEFAULT 'nt-agent.v1',
 os text NOT NULL, arch text NOT NULL, version text NOT NULL, network_scope jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), revoked_at timestamptz
);
UPDATE instance_state SET schema_version=7;
