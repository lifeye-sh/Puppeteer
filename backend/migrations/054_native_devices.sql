-- A device login is one OAuth family, not a trusted hardware identifier.
CREATE INDEX oauth_family_owner ON oauth_token_families(user_id,client_id,id);
CREATE TABLE native_client_devices (
 family_id uuid PRIMARY KEY REFERENCES oauth_token_families(id) ON DELETE CASCADE,
 name text NOT NULL DEFAULT 'Next Terminal 客户端' CHECK(char_length(name) BETWEEN 1 AND 80),
 os text NOT NULL DEFAULT 'unknown',
 arch text NOT NULL DEFAULT 'unknown',
 client_version text NOT NULL DEFAULT 'unknown',
 source_ip text NOT NULL DEFAULT '' CHECK(octet_length(source_ip)<=64),
 registered_at timestamptz NOT NULL DEFAULT now(),
 last_seen_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO native_client_devices(family_id)
 SELECT id FROM oauth_token_families WHERE client_id='00000000-0000-4000-8000-000000000014' AND user_id IS NOT NULL;
UPDATE instance_state SET schema_version=54 WHERE singleton=1;
