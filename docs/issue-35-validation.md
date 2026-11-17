# Issue #35 validation

Implemented against origin/main 394f050. Retention is a new feature, targeting
v0.8.0 after merge (latest existing tag: v0.7.0).

## Scope

The current schema has no activity/audit log tables and the activity API exposes
an empty logs placeholder. Persisted operational history lives in transcode_queue
and download_queue. Maintenance prunes expired terminal rows there, with related
acquisition candidates, while retaining queue records needed by unfinished
requests. No unused log storage or new logging producers were introduced.

Retention configuration is environment-only through pkg/config:
CALLIMACHUS_LOG_RETENTION_DAYS, default 30. The unprefixed alias mentioned in the
issue is deliberately unsupported under AGENTS.md. No database setting or HTTP
mutation path was introduced. Server and worker use stderr, with host file
rotation guidance documented in README.

## Local checks

- `go test -count=1 -v ./...`: passed, no aggregate time limit; no individual
  test over 500 ms or package over 2.5 s.
- `go test -race -timeout 0 ./...`: passed. Packages over 2.5 s were server
  (2.529 s), database (4.034 s), metadata (2.963 s). These include race runtime
  startup/shutdown and instrumentation; their ordinary runs were below the
  investigation threshold. No sleeps or external work were added.
- `go vet ./...`: passed.
- `npm test` in web: all 32 tests passed. SettingsView was 4.643 s under parallel
  suite execution; three tests took 1.375 s, 0.671 s, and 1.038 s. Inspection
  confirmed all userEvent setups use delay:null and there are no timer waits.
  Re-running that file alone with --maxWorkers=1 passed in 1.457 s, with no
  individual test over 500 ms. The tests exercise several settings tabs, inputs,
  and accessibility queries. The observed outliers reflect DOM work plus suite
  contention, not artificial waits; coverage was preserved.
- `npm run build` in web: passed.
- `./scripts/sast.sh`: passed, 352 rules, zero findings.

New database tests cover 10/29/31/60-day timestamps, active job preservation,
recent updates, custom retention, acquisition references, >500-row batching,
rollback of related candidate deletes, timestamp index query plans, WAL reader
snapshots, invalid retention, and cancellation. Scheduler tests advance ticks
through channels without sleeps and cover startup, subsequent passes, cancellation.
