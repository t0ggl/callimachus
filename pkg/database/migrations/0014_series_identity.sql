-- Leave existing series and book links in place. Identity is assigned when
-- a catalog write or a verified provider lookup has enough information.
ALTER TABLE series ADD COLUMN author_id TEXT REFERENCES authors(id) ON DELETE SET NULL;
ALTER TABLE series ADD COLUMN hardcover_id INTEGER;
CREATE UNIQUE INDEX idx_series_hardcover_id ON series(hardcover_id) WHERE hardcover_id IS NOT NULL;
