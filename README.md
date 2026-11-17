# Callimachus

> *Self-hosted eBook & Audiobook manager, library organizer, and automated acquisition system.*

Named after **Callimachus**, the scholar-librarian of the ancient Library of Alexandria who created the *Pinakes* (the world's first comprehensive bibliographic library catalog), Callimachus bridges the gap in modern self-hosted reading libraries.

## Key Capabilities

- **Radarr / Sonarr Model for Books:** Monitored authors, book series tracking, and automated grabbing via Prowlarr.
- **Unified eBooks & Audiobooks:** Full support for EPUB metadata enrichment and decoupled multi-part MP3 to M4B muxing with chapter markers and cover art.
- **First-Class Narrators:** Explicit voice actor tracking, narrator-based filtering, and preferred narrator release monitoring.
- **Deep Metadata Curation:** Deterministic-first multi-source matching (Hardcover, Audnexus, OpenLibrary) with optional AI disambiguation for messy scene releases.
- **Canonical Library Layout:** Fully compatible with Calibre and Audiobookshelf file structures.
- **Multi-User & OIDC Native:** Multi-user support with OIDC authentication (Authentik, Authelia, Keycloak) and single-password admin fallback.
- **Impeccable UI:** High-density, keyboard-driven interface with Overseerr-style user book requests.

## Documentation

- [REQUIREMENTS.md](REQUIREMENTS.md): Comprehensive functional and technical specification.
- [ARCHITECTURE.md](ARCHITECTURE.md): Complete technical architecture, package layout, and system topology.
- [DATA_MODEL.md](DATA_MODEL.md): Complete relational database schema, entity specifications, and ER diagrams.
- [AGENTS.md](AGENTS.md): Standards, constraints, and instructions for agentic development.

## eBook download size limit

Quality & Profiles includes **Maximum eBook File Size (MB)**, defaulting to 100 MB.
Oversized eBook candidates are rejected by the download quality filter; audiobooks
retain the separate general file size limit, which defaults to 1.5 GB (1,536 MB). Sizes use 1 MB = 1,048,576 bytes.
Unknown release sizes remain eligible. Set the limit to 0 to disable it.

`CALLIMACHUS_MAX_EBOOK_SIZE_MB` accepts a non-negative whole number of MB and takes
precedence over the saved setting, locking the corresponding UI control. The
settings API exposes `qualityProfile.maxEbookSizeBytes` in bytes (whole MB values)
and override metadata under `max_ebook_size_mb`. No unprefixed environment alias
is supported.

## Acquisition through Prowlarr

Set the Prowlarr URL and API key in **Settings → Indexers & Prowlarr** (or
`CALLIMACHUS_PROWLARR_URL` and `CALLIMACHUS_PROWLARR_API_KEY`). Configure all
download clients and their categories in Prowlarr, then sync the indexers.
Callimachus does not connect to qBittorrent, Transmission, or SABnzbd directly.
Existing download queue history is preserved during upgrade; stored direct-client
credentials are removed.

Curators and admins can search using `GET /api/releases?query=Author%20Title`.
To acquire a result, send its `guid`, `indexerId`, `title`, `protocol`, and `size`
as JSON to `POST /api/releases/grab`. Callimachus posts the GUID and indexer ID
to Prowlarr's `/api/v1/search`; Prowlarr selects the configured download client.
If Prowlarr's search cache expires, search again before retrying the grab.

Set **Settings → Library & Storage → Completed Download Directory** or
`CALLIMACHUS_COMPLETED_DOWNLOAD_ROOT` to a shared, completed-only directory.
Configure download clients to move finished jobs there atomically, keeping
incomplete downloads elsewhere. Use one top-level directory per release (or a
single finished media file). Exact release names associate queued acquisitions;
unmatched completed media is also ingested. Symlinks and hidden entries are
ignored. Failed imports remain queued with an error and retry on the next scan;
successfully imported paths are recorded to prevent duplicate ingestion.
Leave the directory setting empty to disable ingestion. Callimachus reports the
handoff as queued until the completed files arrive; it does not poll client
progress or remove torrents from the client.

### Book acquisition

Use **Find a book** to search OpenLibrary, Google Books and (with an API key)
Hardcover, then **Add** a confirmed match. Configure Google Books and Hardcover
keys under Settings → Indexers, or use `CALLIMACHUS_GOOGLE_BOOKS_API_KEY` and
`CALLIMACHUS_HARDCOVER_API_KEY`. OpenLibrary requires no key; Google Books and Hardcover are queried when their
API keys are configured. Environment overrides lock their Settings controls.

Acquisition queries Prowlarr's enabled sources and ranks quality-filtered releases
by format preference, seeders, verification and publication year. It dispatches the
highest positive score, tries at most three releases, and rechecks unmatched books
daily. Failed release identities are permanently excluded from automatic retries
with a -100 penalty. **Manual Search** on an acquisition or book detail shows cached
scores and allows refresh and explicit retries. Successful-download caches expire
after 30 days; failure exclusions remain.

Prowlarr delegates transfers to its configured download clients. Completed files
are ingested from the completed-download directory as before. A download client
integration must report terminal transfer failures with authenticated
`POST /api/acquisition/failure`, body
`{"queue_id":"<Callimachus activity queue ID>","error":"reason"}` using a Curator
or Admin session (cookie or Bearer token). This records failure for the background engine to select
the next candidate. Prowlarr itself does not report downstream transfer status.

### Automatic eBook conversion

In Settings → Quality Profile, enable “Automatically convert eBooks to target format”
and choose EPUB (default) or AZW3. Enable the source formats you want to acquire in
Allowed File Formats. Conversion runs in the worker before library ingestion, with
progress and errors in Activity. Original downloads are retained for retries.

The container includes Calibre. Native installations need `ebook-convert` on the
worker's PATH. EPUB output is checked for container, metadata, and manifest resources;
AZW3 is decoded back to EPUB and checked before publishing. DRM-protected or corrupt
inputs fail visibly and are never ingested as successful conversions.

Environment overrides: `CALLIMACHUS_CONVERT_EBOOKS=true` and
`CALLIMACHUS_EBOOK_TARGET_FORMAT=EPUB` (or `AZW3`). These lock their Settings controls.
The optional real Calibre integration check is
`CALLIMACHUS_REAL_CALIBRE=1 go test ./pkg/transcoder -run TestWorkerEbookRealCalibre -v`;
standard tests mock every converter subprocess.

### Log retention and maintenance

The server prunes historical download/import and transcode job records on startup
and every 24 hours. The default retention is **30 days**. Set
`CALLIMACHUS_LOG_RETENTION_DAYS` to a positive integer (1–365000) to override it;
invalid values stop server startup. This is backend, environment-only configuration,
not a database or Settings UI option. Unprefixed `LOG_RETENTION_DAYS` is unsupported.

Only completed, failed, or cancelled jobs whose creation **and last update** are
older than the retention window are removed. Pending/running jobs and download
history still required by unfinished acquisition requests are preserved. Related
cached candidates for available requests are removed with expired download
history; books, media, requests and failed-release tombstones are retained.
Indexed timestamp queries delete at most 500 jobs per transaction, allowing WAL
readers to continue and releasing the SQLite writer between batches. Freed pages
are reused by SQLite; maintenance does not run a blocking `VACUUM`.

There are currently no separate persisted activity/audit log tables (the activity
API's `logs` list is a placeholder). Both server and worker application logs go to
stderr; neither creates log files. If capturing stderr to disk, configure the host
or logging collector to expire logs after 30 days. For a daily file capture using
logrotate, use `daily`, `rotate 30`, `maxage 30`, `missingok`, `notifempty` and
`copytruncate` (the processes do not reopen log files). Run logrotate daily;
container logging drivers with size-based rotation bound space but do not enforce
an exact 30-day age policy, so use an age-aware collector when required.
