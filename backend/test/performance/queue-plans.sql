-- Synthetic distribution, not a production capacity assertion. All data is temporary.
BEGIN;
CREATE TEMP TABLE perf_jobs(id bigint,created_at timestamptz,kind text,state text,cancel_requested bool);
INSERT INTO perf_jobs SELECT n, now()-n*interval '1 second','ssh.command','succeeded',false FROM generate_series(1,100000) n;
INSERT INTO perf_jobs SELECT n+100000,now(),CASE WHEN n%2=0 THEN 'ssh.command' ELSE 'recording.mp4' END,'queued',false FROM generate_series(1,20) n;
CREATE INDEX perf_jobs_ready ON perf_jobs(created_at,id) WHERE state='queued';
ANALYZE perf_jobs;
EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) SELECT id FROM perf_jobs WHERE state='queued' AND kind='ssh.command' AND NOT cancel_requested ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1;
CREATE TEMP TABLE perf_schedules(id bigint,next_run timestamptz,enabled bool,deleted_at timestamptz);
INSERT INTO perf_schedules SELECT n,now()+interval '1 day',true,NULL FROM generate_series(1,10000) n;
CREATE INDEX perf_schedules_due ON perf_schedules(next_run) WHERE enabled AND deleted_at IS NULL;
ANALYZE perf_schedules;
EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) SELECT id FROM perf_schedules WHERE enabled AND deleted_at IS NULL AND next_run<=now() ORDER BY next_run FOR UPDATE SKIP LOCKED LIMIT 1;
ROLLBACK;
