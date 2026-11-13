ALTER TABLE database_assets ADD COLUMN tls_mode text NOT NULL DEFAULT ''
 CHECK (tls_mode IN ('', 'verify-full', 'disable'));
