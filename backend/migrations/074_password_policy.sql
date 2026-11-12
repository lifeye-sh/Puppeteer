INSERT INTO settings(key,typed_value) VALUES
 ('password-strength-type','"recommend"'),
 ('password-strength-policy','"{\"minLength\":12,\"minCharacterType\":2,\"mustNotContainUsername\":true,\"mustNotBePalindrome\":false,\"mustNotWeak\":true}"');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot || jsonb_build_object('password-strength-type','recommend','password-strength-policy','{"minLength":12,"minCharacterType":2,"mustNotContainUsername":true,"mustNotBePalindrome":false,"mustNotWeak":true}'),
 ARRAY['password-strength-type','password-strength-policy']
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
)
UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
UPDATE instance_state SET schema_version=74 WHERE singleton=1;
