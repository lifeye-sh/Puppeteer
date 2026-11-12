ALTER TABLE database_assets ADD COLUMN engine text NOT NULL DEFAULT 'mysql'
 CHECK (engine IN ('mysql','postgresql'));
ALTER TABLE database_passwords ADD COLUMN scram_ready boolean NOT NULL DEFAULT false;
UPDATE instance_state SET schema_version=60 WHERE singleton=1;
