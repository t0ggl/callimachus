CREATE TABLE book_series (
    book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    series_id TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    series_index REAL,
    series_index_display TEXT,
    is_primary INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),
    source TEXT NOT NULL DEFAULT 'import' CHECK (source IN ('scan', 'import', 'match', 'manual_repair')),
    PRIMARY KEY (book_id, series_id)
);
CREATE UNIQUE INDEX idx_book_series_primary ON book_series(book_id) WHERE is_primary = 1;
CREATE INDEX idx_book_series_order ON book_series(series_id, series_index);

INSERT INTO book_series (book_id, series_id, series_index, series_index_display, is_primary)
SELECT id, series_id, series_index, series_index_display, 1
FROM books WHERE series_id IS NOT NULL;

-- The legacy columns drive canonical paths and single-series metadata. Mirror
-- them transactionally so existing writers cannot leave membership reads stale.
CREATE TRIGGER books_insert_series AFTER INSERT ON books WHEN NEW.series_id IS NOT NULL
BEGIN
    INSERT INTO book_series (book_id, series_id, series_index, series_index_display, is_primary)
    VALUES (NEW.id, NEW.series_id, NEW.series_index, NEW.series_index_display, 1);
END;

CREATE TRIGGER books_update_series AFTER UPDATE OF series_id, series_index, series_index_display ON books
WHEN OLD.series_id IS NOT NEW.series_id OR OLD.series_index IS NOT NEW.series_index
     OR OLD.series_index_display IS NOT NEW.series_index_display
BEGIN
    DELETE FROM book_series WHERE book_id = NEW.id AND is_primary = 1 AND series_id IS NOT NEW.series_id;
    INSERT INTO book_series (book_id, series_id, series_index, series_index_display, is_primary)
    SELECT NEW.id, NEW.series_id, NEW.series_index, NEW.series_index_display, 1
    WHERE NEW.series_id IS NOT NULL
    ON CONFLICT (book_id, series_id) DO UPDATE SET
        series_index = excluded.series_index,
        series_index_display = excluded.series_index_display,
        is_primary = 1;
END;

-- An explicit curator removal survives later provider refreshes.
CREATE TABLE book_series_exclusions (
    book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    series_id TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    PRIMARY KEY (book_id, series_id)
);
