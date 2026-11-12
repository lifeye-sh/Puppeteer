ALTER TABLE monitor_rules DROP CONSTRAINT monitor_rules_metric_check;
ALTER TABLE monitor_rules ADD CONSTRAINT monitor_rules_metric_check CHECK(metric IN (
 'agent.cpu','agent.memory','agent.disk','agent.silence','configuration.delay',
 'queue.age','queue.failed','notification.failed','notification.delay','storage.usage',
 'sessions.active','recording.incomplete','backup.failed'));
-- Bound window scans without reading job/recording payloads.
CREATE INDEX jobs_monitor_failed ON jobs(ended_at) WHERE state IN ('failed','partial','unknown');
-- Legacy attempts have no trustworthy finish timestamp: do not fabricate one.
ALTER TABLE notification_deliveries ADD COLUMN last_attempt_at timestamptz;
CREATE INDEX notification_monitor_failed ON notification_deliveries(last_attempt_at) WHERE status='failed';
CREATE INDEX notification_monitor_pending ON notification_deliveries(next_attempt) WHERE status='pending';
CREATE INDEX recordings_monitor_incomplete ON recordings(ended_at) WHERE state NOT IN ('complete','writing');
UPDATE instance_state SET schema_version=72 WHERE singleton=1;
