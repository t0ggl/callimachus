-- Migration 0002_book_requests.sql

-- Overseerr-style book request queue with autoincrement ID and workflow state machine
CREATE TABLE IF NOT EXISTS book_requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id TEXT REFERENCES books(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    author TEXT NOT NULL,
    format TEXT NOT NULL DEFAULT 'both',       -- 'epub', 'm4b', 'both'
    status TEXT NOT NULL DEFAULT 'pending',    -- 'pending', 'approved', 'rejected', 'downloading', 'available'
    rejection_reason TEXT,
    reviewed_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at TIMESTAMP,
    monitored INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_book_requests_status ON book_requests(status);
CREATE INDEX IF NOT EXISTS idx_book_requests_user ON book_requests(user_id);
CREATE INDEX IF NOT EXISTS idx_book_requests_created ON book_requests(created_at);
