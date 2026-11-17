# Issue #19 validation

- Unified Activity renders searching requests, downloading editions, converting
  editions, importing work and completed queue history in one list. Edition IDs
  join phases; request IDs preserve manual search without duplicate request rows.
  Unknown estimates display as unavailable; progress uses download 0–50% and
  conversion/import 50–100% bands, with completed work at 100%.
- Authenticated SSE snapshots refresh additions, updates and removals every two
  seconds. Database failures return an error instead of an empty successful list.
- Conversion names cover Go package/types, API, frontend, pipeline stages and
  documentation. Immutable historical migrations and upgrade regression fixtures
  retain old SQLite names; migration 0009 preserves jobs and updates stage names.
  FFmpeg probing and codec encoding remain technical operations internally.
- Existing environment-only admin authentication was retained. Empty environment
  configuration disables local login, including when legacy SQLite credentials
  exist (covered by the server auth regression suite). No repository deployment
  configuration supplies an admin password. Settings has no password indicators.

## Checks

Passed `go test -v -count=1 ./...`, `go test -race ./...`, `go vet ./...`,
`npm test -- --reporter=verbose` (66 tests), `npm run build`, and
`./scripts/sast.sh` (352 rules, zero findings, including staged new files).
No aggregate suite time limit was imposed. Network and subprocess tests retain
mock transports and injectable runners; real Calibre tests remain opt-in.

Slow-outlier investigation:

- Normal Go run: OIDC login/callback test took 0.59s under concurrent validation.
  It generates a real 2048-bit RSA key and tests signed-token verification with
  mocked HTTP. Other individual tests stayed below 500ms and packages below 2.5s.
- Frontend: discovery/acquisition tests ranged up to 1.107s while race tests and
  SAST ran concurrently. They exercise the real 250ms search debounce; retry and
  stale-response tests intentionally exercise it twice. All user events use
  `delay: null`. No arbitrary test sleeps or external HTTP calls were found.
- Race-instrumented package durations include instrumentation and Go's race exit
  overhead; metadata and indexers perform parsing/scoring fixture suites. The
  normal verbose suite did not exhibit corresponding slow package outliers.
- Impeccable detector: one existing Catalog purple color warning on an unchanged
  heading; no findings on the replacement Activity list. Incumbent styling retained.

Regression coverage includes single-list rendering, removed labels/badges,
phase transitions without duplicate editions, separate formats, errors, unknown
ETA, empty state, full stream replacement, book/edition identity, Settings password
indicator absence, database failure visibility, and upgrade data preservation.
