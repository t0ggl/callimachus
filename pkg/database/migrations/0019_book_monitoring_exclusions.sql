ALTER TABLE books ADD COLUMN ebook_monitoring_excluded INTEGER NOT NULL DEFAULT 0;
ALTER TABLE books ADD COLUMN audiobook_monitoring_excluded INTEGER NOT NULL DEFAULT 0;

-- Earlier releases used cancelled requests to block automatic re-requests.
-- Carry that intent forward unless a newer request superseded the cancellation.
WITH latest AS (
    SELECT r.book_id, f.format,
           MAX(CASE WHEN r.status = 'cancelled' THEN r.id ELSE 0 END) AS cancelled_id,
           MAX(CASE WHEN r.dismissed = 0 AND r.status <> 'cancelled' THEN r.id ELSE 0 END) AS requested_id
    FROM book_requests r
    CROSS JOIN (SELECT 'epub' AS format UNION ALL SELECT 'm4b') f
    WHERE r.book_id IS NOT NULL AND r.format IN (f.format, 'both')
    GROUP BY r.book_id, f.format
), exclusions AS (
    SELECT book_id, MAX(format = 'epub') AS ebook, MAX(format = 'm4b') AS audiobook
    FROM latest WHERE cancelled_id > requested_id GROUP BY book_id
)
UPDATE books
SET ebook_monitoring_excluded = exclusions.ebook,
    audiobook_monitoring_excluded = exclusions.audiobook,
    ebook_monitored = ebook_monitored AND NOT exclusions.ebook,
    audiobook_monitored = COALESCE(audiobook_monitored, ebook_monitored) AND NOT exclusions.audiobook
FROM exclusions WHERE books.id = exclusions.book_id;
