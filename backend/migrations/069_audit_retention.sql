-- Preserve existing retention on upgrade. New categories remain unlimited until configured.
INSERT INTO settings(key,typed_value)
 SELECT 'access-log-saved-limit-days',typed_value FROM settings WHERE key='access-retention-days';
INSERT INTO settings(key,typed_value) VALUES
 ('login-log-saved-limit-days','-1'),('cron-log-saved-limit-days','-1'),
 ('db-sql-log-saved-limit-days','-1'),('operation-log-saved-limit-days','-1'),
 ('filesystem-log-saved-limit-days','-1'),('system-event-saved-limit-days','-1');
WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot || (SELECT jsonb_object_agg(key,typed_value) FROM settings WHERE key LIKE '%-saved-limit-days'),
 ARRAY(SELECT key FROM settings WHERE key LIKE '%-saved-limit-days' ORDER BY key)
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id RETURNING id
) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE singleton=1;
CREATE TABLE audit_holds (
 category text NOT NULL CHECK(category IN ('access','login','cron','sql','operation','filesystem','event')),
 record_id uuid NOT NULL, reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 500),
 actor_id uuid NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(category,record_id)
);
CREATE TABLE audit_retention_previews (
 actor_id uuid NOT NULL REFERENCES users(id), revision bigint NOT NULL REFERENCES configuration_revisions(id),
 key text NOT NULL, days integer NOT NULL CHECK(days=-1 OR days BETWEEN 1 AND 3650), expires_at timestamptz NOT NULL,
 PRIMARY KEY(actor_id,revision,key)
);
ALTER TABLE scheduled_runs ADD COLUMN audit_purged_at timestamptz;
CREATE INDEX sql_logs_retention ON database_sql_logs(finished_at,id) WHERE finished_at IS NOT NULL AND state<>'running';
CREATE INDEX filesystem_logs_retention ON filesystem_logs(ended_at,id) WHERE ended_at IS NOT NULL AND state<>'running';
CREATE INDEX scheduled_runs_retention ON scheduled_runs(id) WHERE audit_purged_at IS NULL;
-- Keep delivery records while allowing expired event payloads to be removed.
ALTER TABLE notification_events DROP CONSTRAINT notification_events_system_event_id_fkey;
ALTER TABLE notification_events ADD CONSTRAINT notification_events_system_event_id_fkey FOREIGN KEY(system_event_id) REFERENCES system_events(id) ON DELETE SET NULL;
UPDATE instance_state SET schema_version=69 WHERE singleton=1;
