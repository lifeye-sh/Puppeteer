CREATE TABLE native_rdp_tickets (
 session_id uuid PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
 family_id uuid NOT NULL REFERENCES oauth_token_families(id) ON DELETE CASCADE,
 ticket_hash text NOT NULL UNIQUE CHECK (ticket_hash ~ '^[0-9a-f]{64}$'),
 expires_at timestamptz NOT NULL,
 consumed_at timestamptz,
 worker_id uuid,
 CHECK ((consumed_at IS NULL) = (worker_id IS NULL))
);
CREATE INDEX native_rdp_ticket_expiry ON native_rdp_tickets(expires_at) WHERE consumed_at IS NULL;
UPDATE instance_state SET schema_version=53 WHERE singleton=1;
