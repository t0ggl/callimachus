CREATE TABLE hidden_library_review (
    book_id TEXT PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
    hidden_path TEXT NOT NULL,
    kept INTEGER NOT NULL DEFAULT 0 CHECK (kept IN (0,1))
);
