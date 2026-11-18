# Issue #4 validation

- `go vet ./...`: passed.
- `go test -v ./...`: passed; no individual test exceeded 500ms in the
  uninstrumented run.
- `go test -v -race ./...`: passed.
- `cd web && npm test`: 13 files / 67 tests passed, including cover URL rendering
  in both the catalog and book detail views.
- `cd web && npm run build`: passed.
- `./scripts/sast.sh`: passed with zero findings (including staged new files).

## Slow-outlier audit

Race instrumentation accounts for the longer timings compared with the normal
run. The new aggregate embedded-cover test took approximately 640ms under the
race detector, covering ten import/rescan scenarios with cloned test databases;
its individual cases are below 500ms. Existing slower tests exercise real schema
migration/locking (database package), 1,001-row history pruning, queue contention,
mocked ebook conversion, mocked indexer synchronization, acquisition fallback,
and the 900-release metadata corpus. They contain no new external work or
artificial waits. These workloads justify retaining their coverage; none exceeded
500ms in the uninstrumented suite. Test database setup uses template cloning.

All newly reachable optional Calibre calls are disabled by injected lookup/runner
hooks in standard catalog, server, downloader, and converter tests. The Calibre
cover extraction test supplies its own runner. EPUB/M4B/MP3 fixture tests run
entirely in Go, without HTTP or host binary invocations. Malformed/unsupported
images, missing covers, canonical JPEG conversion, rescanning, companion
preservation, source integrity, malformed MP4 atoms, and cover-path symlink
containment have regression coverage.
