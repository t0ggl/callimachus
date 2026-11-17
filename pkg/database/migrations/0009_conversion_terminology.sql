-- Historical migrations retain their original names for upgrade compatibility.
ALTER TABLE transcode_queue RENAME TO conversion_queue;
UPDATE conversion_queue SET current_stage = 'preparing' WHERE current_stage = 'probing';
UPDATE conversion_queue SET current_stage = 'converting' WHERE current_stage = 'encoding';
DROP INDEX idx_transcode_status_priority;
CREATE INDEX idx_conversion_status_priority ON conversion_queue(status, priority DESC, created_at ASC);
DROP INDEX idx_transcode_retention;
CREATE INDEX idx_conversion_retention ON conversion_queue(status, updated_at) WHERE status IN ('completed', 'failed', 'cancelled');
