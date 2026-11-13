-- Seed templates once. Do not overwrite existing roles or assign users.
-- System administrator status remains an explicit account type; identity.manage
-- is intentionally not a grantable catalog permission.
WITH created AS (
 INSERT INTO roles(name) VALUES ('管理员'), ('操作员')
 ON CONFLICT (name) DO NOTHING
 RETURNING id,name
)
INSERT INTO role_permissions(role_id,action)
SELECT created.id,permissions.action FROM created CROSS JOIN permissions
WHERE created.name='管理员';
