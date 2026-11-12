CREATE TABLE network_proxies(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),name text NOT NULL,scheme text NOT NULL CHECK(scheme IN ('http','https','socks5','socks5h')),host text NOT NULL,port integer NOT NULL CHECK(port BETWEEN 1 AND 65535),credential_id uuid REFERENCES credentials(id),enabled boolean NOT NULL DEFAULT true,version bigint NOT NULL DEFAULT 1);
ALTER TABLE assets ADD COLUMN proxy_id uuid REFERENCES network_proxies(id);
ALTER TABLE assets ADD CONSTRAINT asset_proxy_route CHECK(proxy_id IS NULL OR (agent_id IS NULL AND jump_id IS NULL AND gateway_group_id IS NULL));
UPDATE instance_state SET schema_version=14;
