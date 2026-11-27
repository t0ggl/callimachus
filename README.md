# Callimachus

[![CI](https://img.shields.io/github/actions/workflow/status/t0ggl/callimachus/ci.yml?branch=main&label=CI)](https://github.com/t0ggl/callimachus/actions/workflows/ci.yml)
[![Container](https://img.shields.io/github/actions/workflow/status/t0ggl/callimachus/build-ghcr.yml?branch=main&label=GHCR)](https://github.com/t0ggl/callimachus/pkgs/container/callimachus)
[![Release](https://img.shields.io/github/v/release/t0ggl/callimachus?label=Release)](https://github.com/t0ggl/callimachus/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/t0ggl/callimachus)](go.mod)

Self-hosted eBook and audiobook collection manager, library organizer, and automated acquisition daemon.

Named after the scholar-librarian of the Library of Alexandria who compiled the *Pinakes*, Callimachus monitors your favorite authors and book series for new releases, searches indexers through Prowlarr, normalizes metadata, converts eBooks and multi-part audiobooks, and organizes your library into a clean, consistent folder hierarchy. It can also upgrade existing books in your library when a higher-quality format becomes available.

## Key Capabilities

- **Automated Acquisition:** Monitor authors, series, and individual books with quality profiles, language filtering, format preferences, and release scoring via Prowlarr.
- **Unified eBooks & Audiobooks:** Enrich EPUB metadata, automatically convert eBooks (`EPUB`/`AZW3`), and combine multi-part MP3 audiobooks into chaptered, tagged `.m4b` files.
- **First-Class Narrators & Series:** Track audiobook voice actors, filter by narrator, and sort series with float-based numbering (`#0.5`, `#1.5`) for novellas and prequels.
- **Multi-Source Metadata:** Deterministic matching across Hardcover, Audnexus, OpenLibrary, and Google Books, with companion `.opf` and cover `.jpg` generation.
- **Canonical Library Layout:** Organizes media into a deterministic, symlink-free directory hierarchy.
- **Multi-User & OIDC Native:** Built-in OpenID Connect support (Pocket ID, Authentik, Authelia, Keycloak) with role-based access control and single-password admin login for testing or single-user setups.

## Quick Start

Run Callimachus with Docker using the prebuilt image from GitHub Container Registry.

The image runs as UID and GID `1000`. Make `/config` and `/books` writable by that identity. For existing bind mounts, check ownership and permissions on both the directories and their existing files. Ingestion also needs write access to the configured completed-download directory to move files into the library.

For a read-only root filesystem in Docker, add `--read-only --tmpfs /tmp:mode=1777`. In Kubernetes, set `readOnlyRootFilesystem: true` and mount an `emptyDir` at `/tmp`. Use writable volumes for `/config` and `/books` and grant access with the pod's `fsGroup` or volume ownership. The conversion worker runs embedded in `callimachus` by default; to offload conversions to a standalone worker container, set `CALLIMACHUS_EMBEDDED_WORKER=false` on the server and run a worker with `--entrypoint /usr/local/bin/callimachus-worker` using the same volume paths and a writable `/tmp`.

### Single-User / Testing (Password Login)

For testing or single-user setups, set `CALLIMACHUS_ADMIN_PASSWORD` to sign in directly as an administrator:

```bash
docker run -d \
  --name callimachus \
  -p 8080:8080 \
  -e CALLIMACHUS_ADMIN_PASSWORD="change-me" \
  -v /path/to/config:/config \
  -v /path/to/books:/books \
  ghcr.io/t0ggl/callimachus:latest
```

Open `http://localhost:8080` in your browser to sign in.

### With OpenID Connect (OIDC)

For multi-user deployments with an identity provider:

```bash
docker run -d \
  --name callimachus \
  -p 8080:8080 \
  -e CALLIMACHUS_OIDC_ISSUER_URL="https://id.example.com" \
  -e CALLIMACHUS_OIDC_CLIENT_ID="callimachus" \
  -e CALLIMACHUS_OIDC_CLIENT_SECRET="your-oidc-client-secret" \
  -e CALLIMACHUS_OIDC_REDIRECT_URL="https://books.example.com/api/auth/oidc/callback" \
  -e CALLIMACHUS_OIDC_ADMIN_GROUPS="admins" \
  -e CALLIMACHUS_PROWLARR_URL="http://prowlarr:9696" \
  -e CALLIMACHUS_PROWLARR_API_KEY="your-prowlarr-api-key" \
  -e CALLIMACHUS_COMPLETED_DOWNLOAD_ROOT="/downloads/completed" \
  -v /path/to/config:/config \
  -v /path/to/books:/books \
  -v /path/to/completed-downloads:/downloads/completed \
  ghcr.io/t0ggl/callimachus:latest
```

## Building from Source

**Prerequisites:** GNU Make, Go 1.26+, and Node.js 26+ with npm.

From the repository root, `make build` installs the locked frontend dependencies, builds the frontend, and compiles `bin/callimachus` and `bin/callimachus-worker`.

```bash
make build

# Start server
CALLIMACHUS_ADMIN_PASSWORD="change-me" ./bin/callimachus --config ./config --library ./books
```

## Configuration

Application settings can be managed in the web UI under **Settings**, or passed via CLI flag or `CALLIMACHUS_`-prefixed environment variable. Specifying a setting via CLI flag or environment variable overrides the database value and disables editing of that field in the UI.

### Core & Acquisition Settings

| Environment Variable | CLI Flag | Default | Description |
| :--- | :--- | :--- | :--- |
| `CALLIMACHUS_CONFIG_DIR` | `--config` | `/config` or `.` | Directory for configuration and default database storage |
| `CALLIMACHUS_DATABASE_PATH` | `--db` | `<config>/callimachus.db` | Path to the SQLite database file |
| `CALLIMACHUS_LIBRARY_ROOT` | `--library` | `/books` or `./books` | Target root directory for the organized book library |
| `CALLIMACHUS_HOST` | `--host` | `0.0.0.0` | HTTP bind address |
| `CALLIMACHUS_PORT` | `--port` | `8080` | HTTP listen port |
| `CALLIMACHUS_PROWLARR_URL` | `--prowlarr-url` | *(empty)* | Prowlarr base URL for indexer syncing, searching, and release grabbing |
| `CALLIMACHUS_PROWLARR_API_KEY` | `--prowlarr-key` | *(empty)* | Prowlarr API key |
| `CALLIMACHUS_COMPLETED_DOWNLOAD_ROOT` | — | *(empty)* | Shared directory where download clients move completed jobs for ingestion |
| `CALLIMACHUS_HARDCOVER_API_KEY` | — | *(empty)* | Hardcover API key for book/series metadata discovery |
| `CALLIMACHUS_GOOGLE_BOOKS_API_KEY` | — | *(empty)* | Google Books API key |
| `CALLIMACHUS_OPENLIBRARY_ENABLED` | — | `true` | Enable keyless OpenLibrary metadata queries |
| `CALLIMACHUS_ALLOWED_LANGUAGES` | — | `en` | Comma-separated list of allowed ISO-639-1 language codes |
| `CALLIMACHUS_ALLOWED_FORMATS` | — | `EPUB,M4B` | Comma-separated list of allowed target formats |
| `CALLIMACHUS_CONVERT_EBOOKS` | — | `false` | Automatically convert acquired eBooks to `CALLIMACHUS_EBOOK_TARGET_FORMAT` |
| `CALLIMACHUS_EBOOK_TARGET_FORMAT` | — | `EPUB` | Target format for eBook conversion (`EPUB` or `AZW3`) |
| `CALLIMACHUS_MAX_EBOOK_SIZE_MB` | — | `100` | Maximum eBook download size in MB (`0` to disable) |
| `CALLIMACHUS_MAX_AUDIOBOOK_SIZE_MB` | — | `1536` | Maximum audiobook download size in MB (`0` to disable) |
| `CALLIMACHUS_LOG_LEVEL` | — | `info` | Logging verbosity (`debug`, `info`, `warn`, `error`) |
| `CALLIMACHUS_LOG_RETENTION_DAYS` | — | `30` | Retention window in days (`1`–`365000`) for pruning finished job records |

### Authentication Settings

Authentication is configured exclusively through environment variables and is never stored in the database or exposed in the web UI.

| Environment Variable | Default | Description |
| :--- | :--- | :--- |
| `CALLIMACHUS_ADMIN_PASSWORD` | *(disabled)* | Enables single-password login for testing or single-user admin setups |
| `CALLIMACHUS_SESSION_TIMEOUT_HOURS` | `720` | Session cookie lifetime in hours |
| `CALLIMACHUS_OIDC_ENABLED` | *(auto)* | Automatically enabled when Issuer URL and Client ID are set |
| `CALLIMACHUS_OIDC_ISSUER_URL` | *(empty)* | OIDC provider issuer URL |
| `CALLIMACHUS_OIDC_CLIENT_ID` | *(empty)* | OIDC client ID |
| `CALLIMACHUS_OIDC_CLIENT_SECRET` | *(empty)* | OIDC client secret |
| `CALLIMACHUS_OIDC_REDIRECT_URL` | *(empty)* | OIDC callback URL (e.g. `https://books.example.com/api/auth/oidc/callback`) |
| `CALLIMACHUS_OIDC_SCOPES` | `openid,profile,email` | Requested OIDC scopes |
| `CALLIMACHUS_OIDC_ADMIN_GROUPS` | *(empty)* | Comma-separated OIDC groups mapped to the `admin` role |
| `CALLIMACHUS_OIDC_CURATOR_GROUPS` | *(empty)* | Comma-separated OIDC groups mapped to the `curator` role |
| `CALLIMACHUS_OIDC_DEFAULT_ROLE` | `user` | Default role assigned to authenticated OIDC users |

## Canonical Library Layout

Callimachus organizes imported and converted media into a deterministic, symlink-free folder structure:

```text
/books/
└── {Author}/
    ├── {Series}/                                   # Present when book belongs to a series
    │   └── {Title} ({Year})/
    │       ├── {Title} ({Year}) - {Author}.epub
    │       ├── {Title} ({Year}) - {Author}.m4b     # Matches eBook basename
    │       ├── {Title} ({Year}) - {Author}.opf     # Metadata sidecar
    │       └── {Title} ({Year}) - {Author}.jpg     # Companion cover artwork
    └── {Title} ({Year})/                           # Standalone title
        ├── {Title} ({Year}) - {Author}.epub
        ├── {Title} ({Year}) - {Author}.m4b
        ├── {Title} ({Year}) - {Author}.opf
        └── {Title} ({Year}) - {Author}.jpg
```

## Development

Tests mock external HTTP services and subprocesses. The backend race tests require a C compiler, and the SAST target uses Semgrep (directly or through `uvx` or `pipx`).
`make test` runs Semgrep SAST first, then the backend and frontend tests. The frontend target installs dependencies automatically.

```bash
make test
```

## Reporting Issues

Found a bug or have a feature request? Please open an issue on [GitHub Issues](https://github.com/t0ggl/callimachus/issues). When reporting a bug, include the steps to reproduce it, your configuration method (Docker or source), and any relevant server log output.

## Documentation

For deep architectural specifications, database schemas, and contributor guidelines, see:

- [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md) — Functional and technical requirements specification
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — System topology, package design, and pipelines
- [docs/DATA_MODEL.md](docs/DATA_MODEL.md) — SQLite relational schema, entity definitions, and ER diagrams
- [AGENTS.md](AGENTS.md) — Development standards, testing rules, and configuration invariants

