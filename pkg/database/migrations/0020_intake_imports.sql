CREATE TABLE intake_attempts (
    source_path TEXT PRIMARY KEY,
    fingerprint TEXT NOT NULL
);

ALTER TABLE conversion_queue ADD COLUMN intake_context TEXT NOT NULL DEFAULT '';
