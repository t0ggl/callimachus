ALTER TABLE book_requests ADD COLUMN dismissed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE book_requests ADD COLUMN counts_toward_quota INTEGER NOT NULL DEFAULT 1;
ALTER TABLE book_requests ADD COLUMN cover_url TEXT NOT NULL DEFAULT '';
ALTER TABLE download_queue ADD COLUMN request_id INTEGER REFERENCES book_requests(id) ON DELETE SET NULL;
ALTER TABLE download_queue ADD COLUMN dismissed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE conversion_queue ADD COLUMN download_id TEXT REFERENCES download_queue(id) ON DELETE SET NULL;
ALTER TABLE conversion_queue ADD COLUMN dismissed INTEGER NOT NULL DEFAULT 0;

UPDATE download_queue SET request_id = (
    SELECT request_id FROM acquisition_candidates WHERE queue_id = download_queue.id LIMIT 1
);
CREATE INDEX idx_download_request ON download_queue(request_id);
CREATE INDEX idx_conversion_download ON conversion_queue(download_id);

CREATE TABLE request_events (
    id INTEGER PRIMARY KEY,
    request_id INTEGER NOT NULL REFERENCES book_requests(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    stage TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL,
    occurred_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_request_events_request ON request_events(request_id, id);

CREATE TRIGGER request_created_event AFTER INSERT ON book_requests
BEGIN
    INSERT INTO request_events(request_id, kind, stage, message)
    VALUES (NEW.id, 'queued', 'queued', 'Request created');
END;

CREATE TRIGGER request_status_event AFTER UPDATE OF status ON book_requests
WHEN OLD.status != NEW.status
BEGIN
    INSERT INTO request_events(request_id, kind, stage, message)
    VALUES (NEW.id, NEW.status,
        CASE NEW.status WHEN 'available' THEN 'available' ELSE '' END,
        CASE NEW.status
            WHEN 'available' THEN 'Book available'
            WHEN 'error' THEN 'Acquisition attempts exhausted'
            WHEN 'cancelled' THEN 'Request cancelled'
            WHEN 'searching' THEN 'Waiting for another release'
            ELSE 'Dispatching release'
        END);
END;

CREATE TRIGGER download_status_event AFTER UPDATE OF status ON download_queue
WHEN OLD.status != NEW.status AND NEW.request_id IS NOT NULL
BEGIN
    INSERT INTO request_events(request_id, kind, stage, message)
    VALUES (NEW.request_id, NEW.status,
        CASE NEW.status WHEN 'queued' THEN 'downloading' WHEN 'importing' THEN 'importing' ELSE '' END,
        CASE NEW.status
            WHEN 'queued' THEN CASE OLD.status WHEN 'importing' THEN 'Import failed and will retry' ELSE 'Waiting for completed download' END
            WHEN 'importing' THEN 'Import started'
            WHEN 'converting' THEN 'Conversion queued'
            WHEN 'completed' THEN 'Import completed'
            WHEN 'failed' THEN 'Download or conversion failed'
            WHEN 'cancelled' THEN 'Download cancelled'
            ELSE 'Download status updated'
        END);
END;

CREATE TRIGGER conversion_status_event AFTER UPDATE OF status ON conversion_queue
WHEN OLD.status != NEW.status AND NEW.download_id IS NOT NULL
BEGIN
    INSERT INTO request_events(request_id, kind, message)
    SELECT request_id, 'conversion_' || NEW.status,
        CASE NEW.status
            WHEN 'pending' THEN 'Conversion will retry'
            WHEN 'processing' THEN 'Conversion started'
            WHEN 'completed' THEN 'Conversion completed'
            WHEN 'failed' THEN 'Conversion attempts exhausted'
            ELSE 'Conversion cancelled'
        END
    FROM download_queue WHERE id = NEW.download_id AND request_id IS NOT NULL;

    UPDATE download_queue SET status = NEW.status, updated_at = CURRENT_TIMESTAMP,
        progress_percent = CASE NEW.status WHEN 'completed' THEN 100 ELSE progress_percent END
    WHERE id = NEW.download_id AND status = 'converting' AND NEW.status IN ('completed', 'failed');
END;

CREATE TABLE cancelled_download_releases (title TEXT PRIMARY KEY);
CREATE TRIGGER cancelled_download_release AFTER UPDATE OF status ON download_queue
WHEN NEW.status = 'cancelled' AND OLD.status != NEW.status
BEGIN
    INSERT OR IGNORE INTO cancelled_download_releases(title) VALUES (NEW.title);
END;

CREATE TRIGGER download_conversion_queued AFTER INSERT ON conversion_queue
WHEN NEW.download_id IS NOT NULL
BEGIN
    UPDATE download_queue SET status = 'converting', edition_id = NEW.edition_id, updated_at = CURRENT_TIMESTAMP
    WHERE id = NEW.download_id AND status = 'importing';
END;

-- Recover links for legacy staged work only when the edition and source path
-- identify one download. An edition alone can have unrelated conversion jobs.
UPDATE conversion_queue AS c SET download_id = (
    SELECT CASE WHEN COUNT(*) = 1 THEN MIN(q.id) END
    FROM download_queue q
    WHERE q.edition_id = c.edition_id AND q.staging_path IS NOT NULL
      AND (c.source_path = q.staging_path OR substr(c.source_path, 1, length(q.staging_path) + 1) = q.staging_path || '/')
)
WHERE c.status IN ('pending','processing');

UPDATE download_queue SET status = 'converting'
WHERE status = 'completed' AND id IN (SELECT download_id FROM conversion_queue WHERE download_id IS NOT NULL);
