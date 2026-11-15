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

5. **Offline-First, Fast Test Suite:**
   - **Imperative Requirement:** Every parser, tokenizer, metadata matcher, Torznab XML reader, OPF generator, and filename formatter must have comprehensive unit tests with zero runtime network requirements.
   - Use recorded fixtures in `testdata/` or `fixtures/`.
   - The test suite must run in under 5 seconds.

6. **Authentication & Multi-User:**
   - Primary: OIDC (Authentik, Authelia, Keycloak) with role mapping (Admin, Curator, User/Requester, Guest).
   - Fallback: `ADMIN_PASSWORD` env var. Login page displays a single password field logging directly in as Admin.

7. **Domain Rules:**
   - Narrators are first-class entities for audiobooks.
   - Series index is float-based (`float64`) to handle novellas (`#2.5`), prequels (`#0.5`), and ARCs.
   - Quality profiles enforce strict language filtering (default English) and format cutoffs.
