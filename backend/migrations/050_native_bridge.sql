CREATE TABLE native_bridge_reservations (
 session_id uuid PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
 actor_id uuid NOT NULL REFERENCES users(id),
 family_id uuid NOT NULL REFERENCES oauth_token_families(id) ON DELETE CASCADE,
 idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 16 AND 64),
 ticket_hash text NOT NULL UNIQUE,
 host_key bytea NOT NULL,
 client_key text NOT NULL,
 expires_at timestamptz NOT NULL,
 consumed_at timestamptz,
 UNIQUE(actor_id,idempotency_key)
);
CREATE INDEX native_bridge_key_expiry ON native_bridge_reservations(expires_at) WHERE octet_length(host_key)>0;
UPDATE instance_state SET schema_version=50 WHERE singleton=1;
