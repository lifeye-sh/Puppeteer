-- Internal disposable fixture only. No host port is published.
CREATE DATABASE inventory;
CREATE DATABASE nt_r16_test;
CREATE ROLE reader LOGIN PASSWORD 'r16-fixture-reader';
CREATE ROLE writer LOGIN PASSWORD 'r16-fixture-writer';
CREATE ROLE member LOGIN PASSWORD 'r16-fixture-member';
GRANT reader TO member;
\connect inventory
REVOKE ALL ON DATABASE inventory FROM PUBLIC;
GRANT CONNECT ON DATABASE inventory TO reader,writer,member;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO reader,writer,member;
CREATE TABLE public.items(id integer PRIMARY KEY,label text);
INSERT INTO public.items VALUES(1,'R16 PostgreSQL fixture'),(2,NULL);
GRANT SELECT ON public.items TO reader;
GRANT SELECT,INSERT,UPDATE,DELETE ON public.items TO writer;
