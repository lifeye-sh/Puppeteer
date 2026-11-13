ALTER TABLE assets ADD COLUMN rdp_security text NOT NULL DEFAULT ''
 CHECK (rdp_security IN ('', 'nla', 'rdp'));
ALTER TABLE assets ADD CONSTRAINT assets_legacy_rdp_security
 CHECK (rdp_security <> 'rdp' OR (protocol = 'rdp' AND cert_fingerprint = ''));
