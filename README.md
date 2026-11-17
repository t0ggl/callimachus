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
