# Callimachus: Project Requirements & System Specification

> **Version:** 1.1.0-final  
> **Status:** Approved Baseline  
> **Target Repository:** `t0ggl/callimachus` (Private GitHub)  
> **Implementation Stack:** Go (Backend, embedded SQLite) + React/Vite/Tailwind (Frontend)

---

## 1. Executive Summary & Core Mission

**Callimachus** (named after the Chief Librarian of the Library of Alexandria who cataloged the ancient world's works) is a self-hosted, multi-user web application and automation daemon for managing, acquiring, and curating collections of eBooks and Audiobooks.

It adopts the proven management paradigm of the `*arr` stack (Radarr, Sonarr, Lidarr):
- **Monitored Entities:** Authors, Book Series, and individual Books/Editions.
- **Automated Acquisition:** Continuous monitoring of indexers via Prowlarr for missing and upcoming releases.
- **Canonical Library Organization:** Deterministic file and directory naming aligned with Calibre and Audiobookshelf standards.
- **Deep Metadata Curation:** Deterministic-first normalization and enrichment, cover embedding, and OPF generation, with an *optional* AI-driven resolution fallback for edge cases.
- **Multi-User Access & Requests:** OIDC-first authentication with a single-password fallback, user-specific wishlists, and a request management workflow (akin to Overseerr/Jellyseerr).
- **Decoupled Audio Transcoder Worker:** Standalone queue worker that processes one transcoding job at a time (multi-part MP3 to M4B), architected for future multi-container horizontal extraction.

---

## 2. Core Architecture & Tech Stack

### 2.1 Backend (Go)
- **Language:** Go 1.23+
- **Architecture:** Clean modular architecture:
  - `cmd/callimachus`: Main server entry point (web server + scheduler + daemon).
  - `cmd/callimachus-worker`: Standalone audio transcode worker (runs embedded by default, can be split into separate container).
  - `pkg/catalog`: Books, authors, series, editions, narrators, and library scanner.
  - `pkg/metadata`: Providers (Hardcover, Audnexus, OpenLibrary, Google Books), sanitizers, scorers, and optional AI disambiguation.
  - `pkg/epub`: EPUB inspection, metadata injection, and OPF generation with high-reliability parser and optional Calibre `ebook-meta` driver.
  - `pkg/transcoder`: FFmpeg runner, M4B muxer, chapter generator (`FFMETADATA1`), and metadata tagger.
  - `pkg/indexers`: Prowlarr Torznab/Newznab client, RSS syncer, and search query builder.
  - `pkg/downloaders`: Completed-directory ingestion, quality profiles, and blocklist.
  - `pkg/auth`: OIDC authenticator, session manager, and single-password fallback.
  - `pkg/database`: SQLite database using modern Go driver (pure Go or cgo-free `modernc.org/sqlite`) with migration engine.
- **Storage:** Single embedded SQLite database (`callimachus.db`) with WAL mode.
- **Filesystem Paths:** Download client and Callimachus share identical filesystem paths (no translation required).

### 2.2 Frontend (React / Vite / Tailwind)
- Designed and polished adhering strictly to the **`impeccable`** skill in `Operate` mode.
- Dense, keyboard-navigable (`/` search, `j`/`k` list nav, shortcuts), responsive across desktop and tablet/mobile.
- Rich book cards, reading order series progress rings, and real-time transcode/download queue progress via WebSockets/SSE.

---

## 3. Domain Model & First-Class Entities

```
+-------------------------------------------------------------+
|                           Author                            |
|  - id, name, sort_name, aliases[], photo_url, biography     |
+------------------------------+------------------------------+
                               | 1..*
                               v
+-------------------------------------------------------------+
|                           Series                            |
|  - id, title, description, total_books, status              |
+------------------------------+------------------------------+
                               | 1..*
                               v
+-------------------------------------------------------------+
|                            Book                             |
|  - id, title, subtitle, series_id, series_index (float)     |
|  - release_date, synopsis, genres[], monitored (bool)       |
+------------------------------+------------------------------+
                               | 1..*
                               v
+-------------------------------------------------------------+
|                        BookEdition                          |
|  - id, book_id, format (epub|m4b|pdf|azw3)                  |
|  - isbn13, asin, hardcover_id, openlibrary_id, goodreads_id |
|  - publisher, publication_date, language (ISO 639-1)        |
|  - narrators[] (First-Class for Audiobooks!)                |
+------------------------------+------------------------------+
                               | 1..*
                               v
+-------------------------------------------------------------+
|                         MediaAsset                          |
|  - id, edition_id, file_path, file_size, checksum           |
|  - audio_codec, bitrate, sample_rate, chapters[]            |
|  - is_verified (bool)                                       |
+-------------------------------------------------------------+
```

### Key Domain Principles
1. **Work vs. Edition Decoupling:** A book work (*The Martian*) links to multiple editions (Kindle EPUB, Audible M4B narrated by Wil Wheaton, Audible M4B narrated by R.C. Bray).
2. **First-Class Narrators:** Audiobooks explicitly associate one or more narrators. Users can filter by narrator, view an author's works by narrator, or prefer releases read by specific voice actors.
3. **Float-Based Series Index:** Series indices are stored as floating point numbers (e.g. `1.0`, `1.5`, `2.0`, `13.0`) with optional display overrides to natively handle novellas, prequels (`0.5`), and multi-book ARCs without sorting bugs.
4. **Normalized Identifiers:** Unified lookup map for `isbn13`, `asin`, `hardcover_id`, `openlibrary_id`, and `goodreads_id`.

---

## 4. Metadata Engine & Curation Pipeline

### 4.1 Deterministic-First Normalization Pipeline
1. **Title & Release Sanitizer:**
   - Strips scene release noise: `Retail`, `Web-Rip`, `V1`, group names, audio bitrates (`64kbps`, `128kbps`), language tags, and packaging tags.
   - Normalizes author formatting: `Last, First` -> `First Last`, strips co-author junk (`Author1 & Author2` handled cleanly).
2. **Multi-Source Metadata Query:**
   - **Hardcover API:** Primary structured book & series catalog.
   - **Audnexus API:** High-fidelity audiobook metadata scraping from Audible ASINs (precise chapter timings, narrator credits, hi-res covers).
   - **OpenLibrary / Google Books:** Broad fallback for ISBN queries and publication dates.
3. **Heuristic Confidence Scorer:**
   - Weighted score: Title Levenshtein similarity ($0.45$), Author token overlap ($0.35$), Year proximity ($0.20$).
   - Score $\ge 0.85$: Automated match and enrichment.
   - Score $< 0.85$: Routes to optional AI agent if enabled, or queues for one-click manual confirmation.
4. **Optional AI Disambiguation (Opt-In):**
   - Strictly optional. Callimachus can run 100% deterministically without any AI API keys.
   - When enabled, ambiguous releases (e.g. obscure anthologies or completely mangled filenames) are evaluated by an LLM prompt to identify the correct book edition and populate missing tags.

### 4.2 EPUB Metadata Reliability & Testing Strategy
- **The Challenge:** EPUB files in the wild have varying versions (EPUB 2.0.1, EPUB 3.0, 3.2), dirty namespaces, malformed XML containers, and fragile zip compression (mimetype file order). In the past, non-Calibre libraries frequently corrupted files.
- **The Callimachus Approach:**
  - **Comprehensive Test Suite:** A large fixture suite of real-world EPUB files (valid, malformed, EPUB2, EPUB3, RTL, and various vendor quirks) tested offline.
  - **Dual Engine Architecture:**
    - *Native Go EPUB Engine:* Zero-dependency pure Go zip/XML parser and writer that strictly preserves package structure, validates XML against schemas, and guarantees zero corruption.
    - *Calibre CLI Fallback (`ebook-meta`):* If `ebook-meta` is present on the host system, Callimachus can optionally delegate EPUB tag writing to Calibre's battle-tested CLI.
- **Sidecar Files:** Always generates `{Title} ({Year}) - {Author}.opf` (Calibre format) and `{Title} ({Year}) - {Author}.jpg` (cover art) alongside the media file.

---

## 5. Decoupled Audio Transcoder Worker

### 5.1 Standalone Worker Architecture
- Transcoding multi-part MP3s into a unified M4B is resource-intensive.
- Callimachus uses a **decoupled queue worker**:
  - Operates as a separate goroutine or standalone process (`callimachus-worker`).
  - Pulls **one job at a time** off a FIFO SQLite-backed persistent queue.
  - Designed for future extraction into separate container instances on worker nodes.
- **Transcoding Pipeline:**
  1. Validates and orders audio parts via M3U playlist or numeric track/filename sorting.
  2. Multi-core parallel transcode of individual parts to AAC-LC using FFmpeg.
  3. Concatenates into a single `.m4b` container without quality degradation.
  4. Injects `FFMETADATA1` chapter markers with accurate timestamps and chapter titles.
  5. Injects embedded front cover art and metadata tags (`artist`, `album`, `title`, `date`, `composer=Narrator`).
  6. Atomic move to target library directory: `{Title} ({Year}) - {Author}.m4b` (never create symlinks).

---

## 6. Acquisition, Indexers & Download Clients

### 6.1 Prowlarr Integration
- Torznab/Newznab categories:
  - `7000 / 7020`: E-Books
  - `3030`: Audiobooks
- Automated RSS sync every $N$ minutes for newly announced monitored releases.
- Scheduled backlog search sweeps for missing monitored books.
- Interactive release search in web UI with parsed quality scores.

### 6.2 Quality & Language Profiles
- Strict language filtering (default: English) to reject foreign-language releases.
- Quality cutoffs:
  - E-Books: EPUB preferred over MOBI/AZW3/PDF.
  - Audiobooks: Single M4B 64kbps+ preferred over multi-part MP3s.

### 6.3 Download Clients & Blocklist
- Configure download clients and categories in Prowlarr; Callimachus requires only the Prowlarr URL and API key.
- Search through Prowlarr and dispatch cached releases via its release download endpoint.
- Ingest finished media from the shared completed-download directory (`CALLIMACHUS_COMPLETED_DOWNLOAD_ROOT`); incomplete downloads must remain elsewhere.
- Release blocklist: permanently blacklists failed or corrupt torrent info-hashes to avoid redownload loops.
- Roadmap: Anna's Archive (MD5/IPFS lookup) and Z-Library direct acquisition.

---

## 7. Canonical Library Layout

All media is saved according to strict canonical rules:
```
/library/books/
└── {Author}/
    └── {Series}/                                (if in a series)
        └── {Title} ({Year})/
            ├── {Title} ({Year}) - {Author}.epub
            ├── {Title} ({Year}) - {Author}.m4b  (same basename as book!)
            ├── {Title} ({Year}) - {Author}.opf  (Calibre metadata)
            └── {Title} ({Year}) - {Author}.jpg  (Front cover)
```
- Standalone titles: `/library/books/{Author}/{Title} ({Year})/{Title} ({Year}) - {Author}.{ext}`
- **Strict Rule:** Never create symlinks. Always use matching basenames for audiobooks and ebooks in the same title folder.

---

## 8. Multi-User Authentication & Requests

### 8.1 Authentication
- **Primary: OpenID Connect (OIDC)**
  - Authentik, Authelia, Keycloak, Google Workspace, GitHub OAuth.
  - Automatic user provisioning from JWT claims (`sub`, `email`, `preferred_username`, `groups`).
- **Fallback: Single Admin Password (`ADMIN_PASSWORD`)**
  - If OIDC is unconfigured, displays a single password input field logging in directly as the Admin user.

### 8.2 Roles & Request Workflow
- **Roles:** Admin, Curator, Requester/User, Guest/Read-only.
- **Request Portal:** Users can search and request missing books, audiobooks, or entire series.
- Adding a confirmed metadata match starts acquisition immediately. There is no approval queue. Failed releases receive a permanent -100 penalty and are excluded from automatic retries; acquisition tries at most three candidates.

---

## 9. Quality, Testing & Offline Mandate

- **100% Offline Testability:** Every parser, tokenizer, metadata matcher, Torznab XML reader, OPF generator, and filename formatter must have comprehensive unit tests with zero runtime network requirements.
- **Fixture Library:** Bundled JSON and XML fixtures simulating real-world release strings (including bizarre torrent names, multi-author books, accented characters, and invalid metadata).
- **Sub-Second Test Runs:** Unit test suite must execute in under 5 seconds.
- **Mock-Driven Integration Testing:** HTTP clients for Prowlarr, Hardcover, Audnexus, and download clients must be interfaced and mockable for reliable offline end-to-end verification.
