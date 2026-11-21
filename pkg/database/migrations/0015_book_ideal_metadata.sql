CREATE TABLE book_ideal_metadata (
    book_id TEXT PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    series TEXT,
    series_index REAL,
    source TEXT NOT NULL DEFAULT 'import',
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
