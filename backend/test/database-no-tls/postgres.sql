CREATE DATABASE inventory;
CREATE ROLE reader LOGIN PASSWORD 'fixture-reader-only';
\connect inventory
REVOKE ALL ON DATABASE inventory FROM PUBLIC;
GRANT CONNECT ON DATABASE inventory TO reader;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO reader;
CREATE TABLE public.items(id integer PRIMARY KEY,label text);
INSERT INTO public.items VALUES(1,'no TLS fixture');
GRANT SELECT ON public.items TO reader;
