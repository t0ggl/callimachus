-- NULL retains author monitoring's legacy behavior of searching both
-- formats until the first format is acquired.
ALTER TABLE books ADD COLUMN audiobook_monitored INTEGER DEFAULT 0;
ALTER TABLE series ADD COLUMN audiobook_monitored INTEGER NOT NULL DEFAULT 0;

-- Carry forward active audiobook requests, including legacy combined requests.
UPDATE books
SET audiobook_monitored = 1,
    monitored = EXISTS (
        SELECT 1 FROM book_requests r WHERE r.book_id = books.id
        AND r.format IN ('epub', 'both') AND r.dismissed = 0 AND r.status IN ('searching', 'downloading')
    )
WHERE monitored = 1 AND EXISTS (
    SELECT 1 FROM book_requests r WHERE r.book_id = books.id
    AND r.format IN ('m4b', 'both') AND r.dismissed = 0 AND r.status IN ('searching', 'downloading')
);

CREATE VIEW missing_monitored_formats AS
SELECT b.id AS book_id, f.format
FROM books b
CROSS JOIN (SELECT 'epub' AS format UNION ALL SELECT 'm4b') f
WHERE (CASE f.format WHEN 'm4b' THEN COALESCE(b.audiobook_monitored, b.monitored) ELSE b.monitored END) = 1
  AND NOT EXISTS (
      SELECT 1 FROM book_editions e JOIN media_assets a ON a.edition_id = e.id
      WHERE e.book_id = b.id AND (e.format IN ('m4b', 'mp3')) = (f.format = 'm4b')
  );
