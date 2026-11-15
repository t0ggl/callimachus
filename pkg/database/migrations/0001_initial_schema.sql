-- Migration 0001_initial_schema.sql

-- 1. Authors
CREATE TABLE authors (
    id TEXT PRIMARY KEY,                       -- UUIDv4 / ULID
    name TEXT NOT NULL,                        -- e.g. "Brandon Sanderson"
    sort_name TEXT NOT NULL,                   -- e.g. "Sanderson, Brandon"
    aliases TEXT NOT NULL DEFAULT '[]',        -- JSON array of alternate names
    photo_url TEXT,
    biography TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_authors_name ON authors(name);
CREATE INDEX idx_authors_sort_name ON authors(sort_name);

-- 2. Narrators (First-class audiobook entities)
CREATE TABLE narrators (
    id TEXT PRIMARY KEY,                       -- UUIDv4 / ULID
    name TEXT NOT NULL,                        -- e.g. "Michael Kramer"
    sort_name TEXT NOT NULL,                   -- e.g. "Kramer, Michael"
    aliases TEXT NOT NULL DEFAULT '[]',        -- JSON array of aliases
    photo_url TEXT,
    biography TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_narrators_name ON narrators(name);

-- 3. Series
CREATE TABLE series (
    id TEXT PRIMARY KEY,                       -- UUIDv4 / ULID
    title TEXT NOT NULL,                       -- e.g. "The Stormlight Archive"
    description TEXT,
    total_books INTEGER,                       -- Expected total count if known
    status TEXT NOT NULL DEFAULT 'ongoing',    -- 'ongoing', 'completed', 'hiatus'
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_series_title ON series(title);

-- 4. Books (Abstract Work)
CREATE TABLE books (
    id TEXT PRIMARY KEY,                       -- UUIDv4 / ULID
    author_id TEXT NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    series_id TEXT REFERENCES series(id) ON DELETE SET NULL,
    series_index REAL,                         -- float64: supports 1.0, 1.5, 2.0, 2.5
    series_index_display TEXT,                 -- Display override, e.g. "#1.5", "Book 2"
    title TEXT NOT NULL,                       -- e.g. "The Way of Kings"
    subtitle TEXT,
    release_date TEXT,                         -- ISO-8601 date YYYY-MM-DD
    synopsis TEXT,
    genres TEXT NOT NULL DEFAULT '[]',         -- JSON array of genre strings
    monitored INTEGER NOT NULL DEFAULT 1,      -- 1 = true, 0 = false
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_books_author_id ON books(author_id);
CREATE INDEX idx_books_series_id ON books(series_id);
CREATE INDEX idx_books_series_order ON books(series_id, series_index);
CREATE INDEX idx_books_monitored ON books(monitored);

-- 5. Book Editions (Physical / Digital Formats)
CREATE TABLE book_editions (
    id TEXT PRIMARY KEY,                       -- UUIDv4 / ULID
    book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    format TEXT NOT NULL,                      -- 'epub', 'm4b', 'pdf', 'azw3'
    isbn13 TEXT,
    asin TEXT,
    hardcover_id TEXT,
    openlibrary_id TEXT,
    goodreads_id TEXT,
    publisher TEXT,
    publication_date TEXT,
    language TEXT NOT NULL DEFAULT 'en',       -- ISO 639-1 code
    monitored INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_editions_book_id ON book_editions(book_id);
CREATE INDEX idx_editions_format ON book_editions(format);
CREATE INDEX idx_editions_isbn13 ON book_editions(isbn13);
CREATE INDEX idx_editions_asin ON book_editions(asin);

-- 6. Edition Narrators (Many-to-Many Narrator mapping)
CREATE TABLE edition_narrators (
    edition_id TEXT NOT NULL REFERENCES book_editions(id) ON DELETE CASCADE,
    narrator_id TEXT NOT NULL REFERENCES narrators(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'primary',      -- 'primary', 'co-narrator', 'full_cast'
    PRIMARY KEY (edition_id, narrator_id)
);
CREATE INDEX idx_edition_narrators_narrator ON edition_narrators(narrator_id);

-- 7. Media Assets (Files stored on disk)
CREATE TABLE media_assets (
    id TEXT PRIMARY KEY,                       -- UUIDv4 / ULID
    edition_id TEXT NOT NULL REFERENCES book_editions(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL UNIQUE,            -- Canonical path on disk
    file_size INTEGER NOT NULL,                -- Bytes
    checksum TEXT,                             -- SHA256 / MD5
    audio_codec TEXT,                          -- 'aac', 'mp3', NULL for epub
    bitrate INTEGER,                           -- In bps (e.g. 128000)
    sample_rate INTEGER,                       -- In Hz (e.g. 44100)
    channels INTEGER,                          -- e.g. 2
    duration_seconds REAL,                     -- Audio duration
    chapters TEXT NOT NULL DEFAULT '[]',       -- JSON array of chapter timestamps & titles
    is_verified INTEGER NOT NULL DEFAULT 0,    -- 1 if passed integrity check
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_media_assets_edition ON media_assets(edition_id);
CREATE UNIQUE INDEX idx_media_assets_path ON media_assets(file_path);

-- 8. Transcode Queue (Decoupled Single-Worker FIFO Pipeline)
CREATE TABLE transcode_queue (
    id TEXT PRIMARY KEY,                       -- UUIDv4 / ULID
    edition_id TEXT NOT NULL REFERENCES book_editions(id) ON DELETE CASCADE,
    source_path TEXT NOT NULL,                 -- Source staging directory or file
    target_path TEXT NOT NULL,                 -- Target canonical .m4b path
    status TEXT NOT NULL DEFAULT 'pending',    -- 'pending', 'processing', 'completed', 'failed', 'cancelled'
    progress_percent REAL NOT NULL DEFAULT 0.0,
    current_stage TEXT NOT NULL DEFAULT 'queued', -- 'probing', 'encoding', 'muxing', 'tagging', 'moving'
    error_message TEXT,
    worker_id TEXT,                            -- ID of worker leasing the job
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    priority INTEGER NOT NULL DEFAULT 0,       -- Higher executes first
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMP,
    completed_at TIMESTAMP
);
CREATE INDEX idx_transcode_status_priority ON transcode_queue(status, priority DESC, created_at ASC);

-- 9. Indexers (Prowlarr Torznab / Newznab sources)
CREATE TABLE indexers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    protocol TEXT NOT NULL,                    -- 'torznab', 'newznab'
    url TEXT NOT NULL,
    api_key TEXT NOT NULL,
    categories TEXT NOT NULL DEFAULT '[7000, 7020, 3030]', -- JSON array of cat IDs
    enabled INTEGER NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 1,
    last_sync_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 10. Download Clients (qBittorrent, Transmission, SABnzbd)
CREATE TABLE download_clients (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,                        -- 'qbittorrent', 'transmission', 'deluge', 'sabnzbd', 'nzbget'
    host TEXT NOT NULL,
    port INTEGER NOT NULL,
    use_ssl INTEGER NOT NULL DEFAULT 0,
    username TEXT,
    password TEXT,
    api_token TEXT,
    category_ebook TEXT NOT NULL DEFAULT 'callimachus-ebooks',
    category_audiobook TEXT NOT NULL DEFAULT 'callimachus-audiobooks',
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 11. Download Queue (Active / Ingested client tasks)
CREATE TABLE download_queue (
    id TEXT PRIMARY KEY,
    client_id TEXT NOT NULL REFERENCES download_clients(id) ON DELETE CASCADE,
    edition_id TEXT REFERENCES book_editions(id) ON DELETE SET NULL,
    download_client_task_id TEXT NOT NULL,     -- Hash or client job ID
    title TEXT NOT NULL,                       -- Release title
    protocol TEXT NOT NULL,                    -- 'torrent', 'usenet'
    info_hash TEXT,                            -- Torrent info-hash
    status TEXT NOT NULL DEFAULT 'queued',     -- 'queued', 'downloading', 'completed', 'staged', 'importing', 'failed'
    progress_percent REAL NOT NULL DEFAULT 0.0,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    staging_path TEXT,                         -- Filesystem path where download finished
    error_message TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_download_queue_client_task ON download_queue(client_id, download_client_task_id);
CREATE INDEX idx_download_queue_status ON download_queue(status);

-- 12. Blocklist (Blacklisted info-hashes & failed releases)
CREATE TABLE blocklist (
    id TEXT PRIMARY KEY,
    info_hash TEXT UNIQUE,                     -- Normalized lowercase 40-char hex
    title TEXT NOT NULL,
    indexer_name TEXT,
    reason TEXT NOT NULL,                      -- e.g. "corrupt audio", "unparseable zip", "manual reject"
    source_format TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_blocklist_hash ON blocklist(info_hash);

-- 13. Users (OIDC & Local admin accounts)
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    email TEXT NOT NULL UNIQUE,
    role TEXT NOT NULL DEFAULT 'requester',     -- 'admin', 'curator', 'requester', 'guest'
    auth_source TEXT NOT NULL DEFAULT 'oidc',  -- 'oidc', 'local'
    oidc_sub TEXT UNIQUE,                      -- Subject ID from OIDC JWT
    password_hash TEXT,                        -- Argon2id hash for local fallback
    avatar_url TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login_at TIMESTAMP
);
CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_oidc_sub ON users(oidc_sub);

-- 14. User Requests (Overseerr-style requests for books/audiobooks)
CREATE TABLE user_requests (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id TEXT REFERENCES books(id) ON DELETE SET NULL,
    requested_title TEXT NOT NULL,
    requested_author TEXT NOT NULL,
    format TEXT NOT NULL DEFAULT 'both',       -- 'epub', 'm4b', 'both'
    status TEXT NOT NULL DEFAULT 'pending',    -- 'pending', 'approved', 'rejected', 'downloading', 'available'
    rejection_reason TEXT,
    requested_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    processed_at TIMESTAMP,
    processed_by TEXT REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX idx_user_requests_status ON user_requests(status);
CREATE INDEX idx_user_requests_user ON user_requests(user_id);

-- 15. Settings (Key-Value Application Configuration)
CREATE TABLE settings (
    key TEXT PRIMARY KEY,                      -- e.g. 'library.path', 'transcoder.bitrate', 'oidc.issuer'
    value TEXT NOT NULL,                       -- String or JSON payload
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
