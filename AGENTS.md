# Callimachus: Agent Guidelines

Welcome to the **Callimachus** project codebase. These guidelines govern all subagents, automated builders, and developers contributing to this repository.

---

## 1. Core Principles & Stack

1. **Technology Stack:**
   - **Backend:** Go 1.23+ (clean modular architecture, standard library first, embedded SQLite with WAL mode).
   - **Frontend:** React / Vite / Tailwind CSS, adhering strictly to the **`impeccable`** skill in `Operate` mode.
   - **Audio Transcoding:** Decoupled single-job queue worker (`pkg/transcoder`, `cmd/callimachus-worker`) pulling off an internal queue. Designed for future multi-container horizontal extraction.

2. **Deterministic-First Metadata & Optional AI:**
   - Always prioritize fast, deterministic, rule-based string sanitizing and multi-source matching (Hardcover, Audnexus, OpenLibrary).
   - AI disambiguation is strictly optional / opt-in. The system must run 100% reliably without requiring AI API keys.

3. **EPUB Reliability & Testing Mandate:**
   - EPUB metadata parsers/writers must be rock-solid and tested against a broad fixture suite of real-world EPUB 2/3 variations.
   - Provide an optional Calibre CLI fallback (`ebook-meta`) when available on the host system.
   - Never corrupt existing EPUB container or mimetype structures.

4. **Canonical Library Organization:**
   - Canonical structure: `{Author}/{Series}/{Title} ({Year})/{Title} ({Year}) - {Author}.{ext}`
   - Standalone titles: `{Author}/{Title} ({Year})/{Title} ({Year}) - {Author}.{ext}`
   - **Strict Audiobook Rule:** Audiobook filenames (`.m4b`) must exactly match the book name in that directory (`{Title} ({Year}) - {Author}.m4b`). Never create symlinks.
   - Always maintain companion `.opf` (Calibre standard) and `.jpg` (cover art).

5. **Testing Guidelines & Standards:**
   - **Zero External Network Calls (Mandatory Mocking):**
     - External HTTP endpoints (Prowlarr, Torznab, Hardcover, Audnexus, OpenLibrary, AI APIs, webhooks, notifiers) must always use mock HTTP transports (`testutil.NewMockHTTPClient`, `httptest.Server`, `testutil.MockTransport`) with `PanicOnUnmocked` enabled.
     - Real network requests to external servers during unit tests are strictly forbidden.
   - **Zero External Binary Invocations (Mandatory Subprocess Mocking):**
     - External CLI tools (such as Calibre's `ebook-meta`, `ffmpeg`, `ffprobe`, etc.) must never be invoked as real OS processes during standard unit test runs.
     - Decouple execution using injectable command runner hooks (e.g. `SetMockCalibreRunner`, runner interfaces) returning fixture data.
     - Host binary execution must be gated behind explicit opt-in environment variables (e.g. `CALLIMACHUS_REAL_CALIBRE=1`) and skipped when missing or running in `-short` mode.
   - **Fast Database Provisioning via Template Cloning (`pkg/testutil`):**
     - Never re-execute full multi-table SQLite DDL migrations (`db.Migrate()`) inside individual unit tests; this causes severe CPU and I/O bloat.
     - Always use `testutil.NewTestDB(t)` or `testutil.PrepareTestDBFile(t, dbPath)`, which clones an in-memory pre-migrated schema snapshot in ~1ms.
     - Reserve real migration execution strictly for migration-specific tests in `pkg/database`.
     - Pass `nil` for `*database.DB` in tests covering disabled features or pre-validation failure paths where the database is never queried.
   - **Eliminate Artificial Sleep & UI Delays:**
     - Avoid arbitrary `time.Sleep()` durations in concurrency and worker lifecycle tests; coordinate using channels, sync primitives, or small poll intervals (<=100ms).
     - In frontend Vitest suites, always initialize user events with `userEvent.setup({ delay: null })` to prevent synthetic typing delays from inflating test execution time.
   - **Mandatory Slow-Outlier Audit Before Merging:**
     - Before merging any branch or opening a pull request, run `go test -v ./...` and `npm test` to audit test execution durations.
     - Any single test taking > 500ms or package taking > 2.5s is an outlier bottleneck that must be investigated and resolved before merging.
     - Ensure the entire repository test suite executes in under 5 seconds.

6. **Authentication & Multi-User:**
   - Primary: OIDC (Authentik, Authelia, Keycloak) with role mapping (Admin, Curator, User/Requester, Guest), configured **exclusively** via `CALLIMACHUS_OIDC_*` environment variables (`CALLIMACHUS_OIDC_ISSUER_URL`, `CALLIMACHUS_OIDC_CLIENT_ID`, `CALLIMACHUS_OIDC_CLIENT_SECRET`, `CALLIMACHUS_OIDC_REDIRECT_URL`, `CALLIMACHUS_OIDC_SCOPES`, `CALLIMACHUS_OIDC_ADMIN_GROUPS`, `CALLIMACHUS_OIDC_CURATOR_GROUPS`, `CALLIMACHUS_OIDC_DEFAULT_ROLE`, `CALLIMACHUS_OIDC_ENABLED`).
   - Fallback & Sessions: `CALLIMACHUS_ADMIN_PASSWORD` and `CALLIMACHUS_SESSION_TIMEOUT_HOURS` env vars. Login page displays a single password field logging directly in as Admin.
   - Auth/OIDC configuration is never persisted in SQLite, exposed on `/api/settings`, editable in the Settings UI, or accepted via CLI flags.

7. **Domain Rules:**
   - Narrators are first-class entities for audiobooks.
   - Series index is float-based (`float64`) to handle novellas (`#2.5`), prequels (`#0.5`), and ARCs.
   - Quality profiles enforce strict language filtering (default English) and format cutoffs.

8. **Configuration & Environment Variable Overrides:**
   - **Mandatory `CALLIMACHUS_` Prefix (No Exceptions):** Every settings/configuration environment variable in Callimachus MUST be prefixed with `CALLIMACHUS_` (e.g., `CALLIMACHUS_LIBRARY_ROOT`, `CALLIMACHUS_OIDC_ISSUER_URL`, `CALLIMACHUS_ADMIN_PASSWORD`). Never introduce or support unprefixed aliases (such as bare `OIDC_ISSUER_URL` or `OIDC_CLIENT_ID`).
   - **Standardized Naming & Automatic Env Var Derivation:** All DB-configurable settings must be registered in `pkg/config` using a canonical `lowercase_snake_case` key (e.g., `library_root`, `prowlarr_url`, `max_file_size_mb`). The corresponding environment variable `CALLIMACHUS_{SETTING_NAME}` (uppercased `snake_case`, e.g., `CALLIMACHUS_LIBRARY_ROOT`, `CALLIMACHUS_PROWLARR_URL`) is derived automatically via `pkg/config.EnvVarName` / `pkg/config.LookupEnvForSetting` so no setting can be missed or diverge.
   - **Absolute Precedence:** Whenever a configuration environment variable is set (non-empty), it takes absolute precedence over values stored in the SQLite `settings` table and built-in defaults across all server and worker workflows.
   - **No Ad-Hoc Env Lookups:** Runtime configuration must be resolved through `pkg/config` rather than scattered `os.Getenv` calls (excluding explicit test opt-in gates like `CALLIMACHUS_REAL_CALIBRE`).
   - **API Metadata:** `GET /api/settings` must expose override metadata (`isOverridden: boolean`, `overriddenBy: string`) for every configurable setting (excluding env-only auth/OIDC settings, which are never exposed or mutable over HTTP).
   - **UI Enforcement:** In `SettingsView.tsx`, any setting with `isOverridden: true` must disable/grey out the corresponding input, toggle, or selector and display an explanatory lock badge or note (e.g., *"Managed via environment variable {overriddenBy} and cannot be modified here."*).
   - **Write Protection:** `PUT /api/settings` must ignore/refuse database mutations for any setting currently overridden by an environment variable, and `PUT`/`POST /api/settings` must reject any auth/OIDC payload.
