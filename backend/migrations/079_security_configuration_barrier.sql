CREATE TABLE configuration_security (
 singleton integer PRIMARY KEY CHECK(singleton=1),
 revision bigint NOT NULL REFERENCES configuration_revisions(id),
 enforced jsonb NOT NULL,
 required_nodes text[] NOT NULL DEFAULT '{}',
 pending boolean NOT NULL DEFAULT false
);
INSERT INTO configuration_security(singleton,revision,enforced)
 SELECT 1,h.revision,(SELECT jsonb_object_agg(key,value) FROM jsonb_each(r.snapshot) WHERE key IN ('login-lock-enabled','login-lock-failed-duration','login-lock-failed-times','login-lock-account-duration','login-lock-ip-duration','login-force-totp-enabled','password-expiration-period','password-strength-type','password-strength-policy')) FROM configuration_head h JOIN configuration_revisions r ON r.id=h.revision;
UPDATE instance_state SET schema_version=79 WHERE singleton=1;
