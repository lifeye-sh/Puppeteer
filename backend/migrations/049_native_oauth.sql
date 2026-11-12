ALTER TABLE oauth_server_settings ADD COLUMN native_enabled boolean NOT NULL DEFAULT false;
INSERT INTO oauth_clients(id,name,secret_hash,redirect_uris,scopes,allow_all_users,enabled)
VALUES('00000000-0000-4000-8000-000000000014','Next Terminal 原生客户端','!public-native','{}',ARRAY['assets:read','sessions:connect','offline_access'],true,true);
UPDATE instance_state SET schema_version=49 WHERE singleton=1;
