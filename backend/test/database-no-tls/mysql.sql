CREATE DATABASE inventory;
CREATE USER 'reader'@'%' IDENTIFIED BY 'fixture-reader-only';
CREATE USER 'writer'@'%' IDENTIFIED BY 'fixture-writer-only';
CREATE TABLE inventory.items(id INT PRIMARY KEY,label VARCHAR(80));
INSERT INTO inventory.items VALUES(1,'no TLS fixture');
GRANT SELECT ON inventory.* TO 'reader'@'%';
GRANT SELECT,INSERT,UPDATE,DELETE ON inventory.* TO 'writer'@'%';
