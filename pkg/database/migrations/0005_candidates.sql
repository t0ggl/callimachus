CREATE TABLE book_requests_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 book_id TEXT REFERENCES books(id) ON DELETE SET NULL,
 title TEXT NOT NULL, author TEXT NOT NULL, format TEXT NOT NULL DEFAULT 'both',
 status TEXT NOT NULL DEFAULT 'searching', monitored INTEGER NOT NULL DEFAULT 1,
 created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
 year INTEGER NOT NULL DEFAULT 0, attempts INTEGER NOT NULL DEFAULT 0,
 searched_at TIMESTAMP, completed_at TIMESTAMP
);
INSERT INTO book_requests_new(id,user_id,book_id,title,author,format,status,monitored,created_at,updated_at)
SELECT id,user_id,book_id,title,author,format,
 CASE status WHEN 'approved' THEN 'searching' WHEN 'pending' THEN 'searching' WHEN 'rejected' THEN 'error' ELSE status END,
 monitored,created_at,updated_at FROM book_requests;
DROP TABLE book_requests;
ALTER TABLE book_requests_new RENAME TO book_requests;
CREATE INDEX idx_book_requests_status ON book_requests(status);
CREATE INDEX idx_book_requests_user ON book_requests(user_id);
CREATE TABLE acquisition_candidates (
 request_id INTEGER NOT NULL REFERENCES book_requests(id) ON DELETE CASCADE,
 release_key TEXT NOT NULL, release_json TEXT NOT NULL, score_json TEXT NOT NULL,
 score INTEGER NOT NULL, format TEXT NOT NULL, queue_id TEXT,
 PRIMARY KEY(request_id,release_key)
);
-- Separate tombstones survive cache eviction and metadata refreshes.
CREATE TABLE failed_releases (release_key TEXT PRIMARY KEY, penalty INTEGER NOT NULL DEFAULT -100);

-- Consolidate the unused first-generation request table before removing review data.
INSERT INTO book_requests(user_id,book_id,title,author,format,status,created_at,updated_at)
SELECT user_id,book_id,requested_title,requested_author,format,
 CASE status WHEN 'available' THEN 'available' WHEN 'downloading' THEN 'downloading' WHEN 'rejected' THEN 'error' ELSE 'searching' END,
 requested_at,COALESCE(processed_at,requested_at) FROM user_requests;
DROP TABLE user_requests;
UPDATE book_requests SET completed_at=updated_at WHERE status='available';
