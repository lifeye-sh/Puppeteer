ALTER TABLE resource_registry DROP CONSTRAINT resource_registry_kind_check;
ALTER TABLE resource_registry ADD CONSTRAINT resource_registry_kind_check CHECK(kind IN ('asset','group','website','database'));
CREATE TABLE database_assets (
 id uuid PRIMARY KEY, kind text NOT NULL DEFAULT 'database' CHECK(kind='database'),
 alias text NOT NULL UNIQUE, host text NOT NULL, port integer NOT NULL CHECK(port BETWEEN 1 AND 65535),
 database_name text NOT NULL, readonly_credential_id uuid NOT NULL REFERENCES credentials(id),
 write_credential_id uuid REFERENCES credentials(id), tls_server_name text NOT NULL, tls_ca text NOT NULL,
 description text NOT NULL DEFAULT '', connect_timeout integer NOT NULL DEFAULT 10 CHECK(connect_timeout BETWEEN 1 AND 60),
 FOREIGN KEY(id,kind) REFERENCES resource_registry(id,kind)
);
INSERT INTO permissions VALUES ('database.read','database-asset','数据库资产查看'),('database.audit','database-sql-log','数据库审计查看'),('database.approve','db-work-order','SQL 工单审批');
UPDATE instance_state SET schema_version=33 WHERE singleton=1;
