ALTER TABLE ai_turns ADD COLUMN revision bigint NOT NULL DEFAULT 0;
ALTER TABLE ai_turns ADD COLUMN cancel_requested boolean NOT NULL DEFAULT false;
ALTER TABLE ai_settings ADD COLUMN readonly_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE ai_conversations ADD COLUMN context_type text NOT NULL DEFAULT '' CHECK(context_type IN ('','asset','session'));
ALTER TABLE ai_conversations ADD COLUMN context_id text NOT NULL DEFAULT '';
CREATE TABLE ai_asset_access (
 asset_id uuid PRIMARY KEY REFERENCES assets(id) ON DELETE CASCADE,
 enabled boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1
);
UPDATE instance_state SET schema_version=58 WHERE singleton=1;
