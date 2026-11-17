-- Terminal queue rows hold conversion output and download/import history.
CREATE INDEX idx_transcode_retention ON transcode_queue(status, updated_at)
 WHERE status IN ('completed', 'failed', 'cancelled');
CREATE INDEX idx_download_retention ON download_queue(status, updated_at)
 WHERE status IN ('completed', 'failed', 'cancelled');
CREATE INDEX idx_candidates_queue ON acquisition_candidates(queue_id);
