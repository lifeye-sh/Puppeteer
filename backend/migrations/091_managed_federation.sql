ALTER TABLE oidc_identity_providers ADD COLUMN settings_managed boolean NOT NULL DEFAULT false;
ALTER TABLE oidc_identity_providers ADD COLUMN configuration_secret_id uuid REFERENCES configuration_secrets(id);
ALTER TABLE oidc_identity_providers ADD COLUMN scopes text[] NOT NULL DEFAULT '{openid}';
CREATE UNIQUE INDEX oidc_providers_managed_kind ON oidc_identity_providers(kind) WHERE settings_managed;
INSERT INTO settings(key,typed_value) VALUES
 ('oidc-enabled','false'),('oidc-binding-enabled','false'),('oidc-issuer','""'),('oidc-client-id','""'),('oidc-client-secret','""'),('oidc-redirect-uri','""'),('oidc-scopes','"openid"'),
 ('wechat-work-enabled','false'),('wechat-work-binding-enabled','false'),('wechat-work-corp-id','""'),('wechat-work-agent-id','""'),('wechat-work-secret','""'),('wechat-work-redirect-uri','""'),
 ('wechat-work-api-url','"https://qyapi.weixin.qq.com"'),('wechat-work-authorization-url','"https://login.work.weixin.qq.com/wwlogin/sso/login"');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||(SELECT jsonb_object_agg(key,typed_value) FROM settings WHERE key LIKE 'wechat-work-%' OR (key LIKE 'oidc-%' AND key NOT LIKE 'oidc-server-%')),
 ARRAY['oidc-enabled','oidc-binding-enabled','oidc-issuer','oidc-client-id','oidc-client-secret','oidc-redirect-uri','oidc-scopes','wechat-work-enabled','wechat-work-binding-enabled','wechat-work-corp-id','wechat-work-agent-id','wechat-work-secret','wechat-work-redirect-uri','wechat-work-api-url','wechat-work-authorization-url']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1),enforced=enforced||'{"oidc-enabled":false,"oidc-binding-enabled":false,"wechat-work-enabled":false,"wechat-work-binding-enabled":false}'::jsonb WHERE singleton=1;
UPDATE instance_state SET schema_version=91 WHERE singleton=1;
