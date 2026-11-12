package pgproxy

import "testing"

func TestPostgresASTPolicy(t *testing.T) {
	for _, sql := range []string{"SELECT id,label FROM public.items ORDER BY id", "SELECT $1::int4, $2::text", "WITH x AS (SELECT 1 AS n) SELECT n FROM x", "SELECT count(*) FROM public.items WHERE id >= 1", "SELECT current_database(),version()", "SHOW transaction_read_only", "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT x", "ROLLBACK TO x", "SET application_name='test'", "SET extra_float_digits=3", "SET client_encoding='UTF8'", "COPY public.items TO STDOUT", "COPY (SELECT id FROM public.items) TO STDOUT WITH (FORMAT csv)", "-- empty"} {
		if e := ValidateSQL(sql); e != nil {
			t.Errorf("allowed query rejected: %s", sql)
		}
	}
	for _, sql := range []string{"SELECT 1; SELECT 2", "WITH x AS (DELETE FROM public.items RETURNING *) SELECT * FROM x", "SELECT * INTO other FROM public.items", "SELECT * FROM public.items FOR UPDATE", "SELECT set_config('transaction_read_only','off',false)", "SELECT public.count(*) FROM public.items", "SELECT pg_read_file('/etc/passwd')", "SELECT 1::public.customtype", "COPY public.items FROM STDIN", "COPY public.items TO '/tmp/data'", "COPY public.items TO PROGRAM 'id'", "SET default_transaction_read_only=off", "RESET ALL", "SET ROLE postgres", "BEGIN READ WRITE", "DO $$BEGIN END$$", "SELECT * FROM public.items WHERE id OPERATOR(public.=) 1", "garbage"} {
		if e := ValidateSQL(sql); e == nil {
			t.Errorf("unsafe query accepted: %s", sql)
		}
	}
}
