DROP TRIGGER IF EXISTS download_status_event;
DROP TRIGGER IF EXISTS conversion_status_event;

CREATE TRIGGER download_status_event AFTER UPDATE OF status ON download_queue
WHEN OLD.status != NEW.status AND NEW.request_id IS NOT NULL
BEGIN
    INSERT INTO request_events(request_id, kind, stage, message)
    VALUES (NEW.request_id, NEW.status,
        CASE NEW.status WHEN 'queued' THEN 'downloading' WHEN 'importing' THEN 'importing' ELSE '' END,
        CASE NEW.status
            WHEN 'queued' THEN CASE OLD.status
                WHEN 'importing' THEN CASE WHEN COALESCE(TRIM(NEW.error_message), '') != ''
                    THEN 'Import failed and will retry: ' || TRIM(NEW.error_message)
                    ELSE 'Import failed and will retry' END
                ELSE 'Waiting for completed download' END
            WHEN 'importing' THEN 'Import started'
            WHEN 'converting' THEN 'Conversion queued'
            WHEN 'completed' THEN 'Import completed'
            WHEN 'failed' THEN CASE WHEN COALESCE(TRIM(NEW.error_message), '') != ''
                THEN 'Download or conversion failed: ' || TRIM(NEW.error_message)
                ELSE 'Download or conversion failed' END
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
            WHEN 'pending' THEN CASE WHEN COALESCE(TRIM(NEW.error_message), '') != ''
                THEN 'Conversion will retry: ' || TRIM(NEW.error_message)
                ELSE 'Conversion will retry' END
            WHEN 'processing' THEN 'Conversion started'
            WHEN 'completed' THEN 'Conversion completed'
            WHEN 'failed' THEN CASE WHEN COALESCE(TRIM(NEW.error_message), '') != ''
                THEN 'Conversion attempts exhausted: ' || TRIM(NEW.error_message)
                ELSE 'Conversion attempts exhausted' END
            ELSE 'Conversion cancelled'
        END
    FROM download_queue WHERE id = NEW.download_id AND request_id IS NOT NULL;

    UPDATE download_queue SET status = NEW.status, updated_at = CURRENT_TIMESTAMP,
        progress_percent = CASE NEW.status WHEN 'completed' THEN 100 ELSE progress_percent END,
        error_message = CASE WHEN NEW.status = 'failed' AND COALESCE(TRIM(NEW.error_message), '') != ''
            THEN NEW.error_message ELSE error_message END
    WHERE id = NEW.download_id AND status = 'converting' AND NEW.status IN ('completed', 'failed');
END;
