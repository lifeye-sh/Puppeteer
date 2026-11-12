INSERT INTO settings(key,typed_value) SELECT 'disable-password-login',to_jsonb(NOT password_enabled) FROM identity_login_policy WHERE singleton=1;
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||jsonb_build_object('disable-password-login',NOT p.password_enabled),ARRAY['disable-password-login'] FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id CROSS JOIN identity_login_policy p RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1),enforced=enforced||jsonb_build_object('disable-password-login',(SELECT NOT password_enabled FROM identity_login_policy WHERE singleton=1)) WHERE singleton=1;
UPDATE instance_state SET schema_version=82 WHERE singleton=1;
