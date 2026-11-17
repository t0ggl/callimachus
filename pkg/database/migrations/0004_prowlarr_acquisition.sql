-- Preserve existing queue history while removing direct client credentials/configuration.
CREATE TABLE download_queue_new (
 id TEXT PRIMARY KEY,
 client_id TEXT NOT NULL DEFAULT 'prowlarr',
 edition_id TEXT REFERENCES book_editions(id) ON DELETE SET NULL,
 download_client_task_id TEXT NOT NULL,
 title TEXT NOT NULL,
 protocol TEXT NOT NULL,
 info_hash TEXT,
 status TEXT NOT NULL DEFAULT 'queued',
 progress_percent REAL NOT NULL DEFAULT 0,
 size_bytes INTEGER NOT NULL DEFAULT 0,
 staging_path TEXT,
 error_message TEXT,
 created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO download_queue_new SELECT * FROM download_queue;
DROP TABLE download_queue;
ALTER TABLE download_queue_new RENAME TO download_queue;
CREATE INDEX idx_download_queue_client_task ON download_queue(client_id, download_client_task_id);
CREATE INDEX idx_download_queue_status ON download_queue(status);
DROP TABLE download_clients;
