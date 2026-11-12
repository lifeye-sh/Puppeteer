ALTER TABLE recording_chunks DROP CONSTRAINT recording_chunks_kind_check;
ALTER TABLE recording_chunks ADD COLUMN media_sha256 bytea;
ALTER TABLE recording_chunks ADD CONSTRAINT recording_chunks_kind_check CHECK (
    (kind IN ('output','resize','exec-output','graphics') AND media_sha256 IS NULL)
    OR (kind='mp4' AND media_sha256 IS NOT NULL AND octet_length(media_sha256)=32
        AND octet_length(payload) BETWEEN 1 AND 16777216)
);
UPDATE instance_state SET schema_version=56 WHERE singleton=1;
