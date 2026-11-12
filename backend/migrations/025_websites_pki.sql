ALTER TABLE resource_registry DROP CONSTRAINT resource_registry_kind_check;
ALTER TABLE resource_registry ADD CONSTRAINT resource_registry_kind_check CHECK(kind IN ('asset','group','website'));
CREATE TABLE certificates (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),name text NOT NULL,kind text NOT NULL CHECK(kind IN ('server','client_ca','client')),source text NOT NULL CHECK(source IN ('self_signed','import','acme','issued')),
 pem text NOT NULL,key_sealed bytea NOT NULL,fingerprint text NOT NULL UNIQUE,dns_names text[] NOT NULL DEFAULT '{}',not_before timestamptz NOT NULL,not_after timestamptz NOT NULL,
 parent_id uuid REFERENCES certificates(id),user_id uuid REFERENCES users(id),revoked_at timestamptz,last_used_at timestamptz,version bigint NOT NULL DEFAULT 1,created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE website_settings (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),default_certificate_id uuid REFERENCES certificates(id),console_url text NOT NULL DEFAULT 'http://backend.localhost:5174',
 api_trusted_proxies text[] NOT NULL DEFAULT '{}',website_trusted_proxies text[] NOT NULL DEFAULT '{}',version bigint NOT NULL DEFAULT 1
);
INSERT INTO website_settings(singleton) VALUES(true);
CREATE TABLE websites (
 id uuid PRIMARY KEY REFERENCES resource_registry(id),group_id uuid REFERENCES resource_groups(id),domain text NOT NULL UNIQUE,entry_path text NOT NULL DEFAULT '/',upstream text NOT NULL,
 mode text NOT NULL DEFAULT 'private' CHECK(mode IN ('private','public','temporary_ip')),public_password_hash text NOT NULL DEFAULT '',policy jsonb NOT NULL DEFAULT '{}',
 certificate_id uuid REFERENCES certificates(id),client_ca_id uuid REFERENCES certificates(id),upstream_ca text NOT NULL DEFAULT '',
 agent_id uuid REFERENCES agent_gateway_configs(id),jump_id uuid REFERENCES ssh_jump_hosts(id),gateway_group_id uuid REFERENCES gateway_groups(id),proxy_id uuid REFERENCES network_proxies(id),
 request_headers jsonb NOT NULL DEFAULT '{}',response_headers jsonb NOT NULL DEFAULT '{}',rewrites jsonb NOT NULL DEFAULT '[]',audit_enabled boolean NOT NULL DEFAULT true,
 temp_ttl_minutes integer NOT NULL DEFAULT 30 CHECK(temp_ttl_minutes BETWEEN 1 AND 1440),temp_max_minutes integer NOT NULL DEFAULT 120 CHECK(temp_max_minutes BETWEEN 1 AND 10080),temp_auto_renew boolean NOT NULL DEFAULT false
);
CREATE TABLE website_temporary_ips(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),website_id uuid NOT NULL REFERENCES websites(id),user_id uuid NOT NULL REFERENCES users(id),ip inet NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL,absolute_expires_at timestamptz NOT NULL,revoked_at timestamptz);
CREATE INDEX website_temporary_ip_lookup ON website_temporary_ips(website_id,ip) WHERE revoked_at IS NULL;
CREATE TABLE website_login_codes(digest text PRIMARY KEY,website_id uuid NOT NULL REFERENCES websites(id),user_id uuid NOT NULL REFERENCES users(id),state_digest text NOT NULL,return_path text NOT NULL,expires_at timestamptz NOT NULL,consumed_at timestamptz);
CREATE TABLE website_sessions(digest text PRIMARY KEY,website_id uuid NOT NULL REFERENCES websites(id),user_id uuid REFERENCES users(id),password_version bigint,expires_at timestamptz NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE website_access_logs(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,website_id uuid NOT NULL REFERENCES websites(id),user_id uuid REFERENCES users(id),client_ip text NOT NULL,method text NOT NULL,path text NOT NULL,status integer NOT NULL,bytes bigint NOT NULL DEFAULT 0,duration_ms bigint NOT NULL DEFAULT 0,reason text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX website_access_logs_time ON website_access_logs(created_at DESC);
CREATE TABLE acme_accounts(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),name text NOT NULL,directory_url text NOT NULL,email text NOT NULL,ca_pem text NOT NULL DEFAULT '',key_sealed bytea NOT NULL,account_url text NOT NULL DEFAULT '',version bigint NOT NULL DEFAULT 1);
CREATE TABLE dns_providers(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),name text NOT NULL,zone text NOT NULL,server text NOT NULL,key_name text NOT NULL,secret_sealed bytea NOT NULL,version bigint NOT NULL DEFAULT 1);
CREATE TABLE certificate_orders(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),name text NOT NULL,account_id uuid NOT NULL REFERENCES acme_accounts(id),provider_id uuid NOT NULL REFERENCES dns_providers(id),domains text[] NOT NULL,certificate_id uuid REFERENCES certificates(id),status text NOT NULL DEFAULT 'pending',auto_renew boolean NOT NULL DEFAULT true,renew_before_days integer NOT NULL DEFAULT 30 CHECK(renew_before_days BETWEEN 1 AND 60),next_attempt timestamptz NOT NULL DEFAULT now(),lease_token text NOT NULL DEFAULT '',lease_until timestamptz,attempts integer NOT NULL DEFAULT 0,last_error text NOT NULL DEFAULT '',version bigint NOT NULL DEFAULT 1,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE certificate_order_events(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,order_id uuid NOT NULL REFERENCES certificate_orders(id),status text NOT NULL,message text NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE acme_dns_records(order_id uuid NOT NULL REFERENCES certificate_orders(id),name text NOT NULL,value text NOT NULL,PRIMARY KEY(order_id,name,value));
INSERT INTO permissions(action,menu_key,label) VALUES('website.read','website','网站查看'),('certificate.read','certificate','证书查看');
UPDATE instance_state SET schema_version=25;
