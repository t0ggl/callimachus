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
