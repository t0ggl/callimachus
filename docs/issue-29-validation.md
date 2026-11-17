# Issue #29 validation

- Based on `origin/main` at `b87797bf4819e467c558f6ac9bb219dca35d6d23`.
- Failure penalties are -100 per the user clarification, with an independent
  permanent exclusion from automatic selection. This exceeds the maximum
  preferential score of 75 and makes every failed candidate negative.
- `go test -v ./...`: passed; 445 tests/subtests. The slowest observed tests were
  OIDC verification (0.26s), the 900-release parser fixture (0.25s), and command
  cancellation (0.20s). No tests exceeded 500ms or packages exceeded 2.5s.
- `go vet ./...` and `go test -race ./pkg/requests ./cmd/callimachus`: passed.
- `npm test -- --reporter=verbose`: 31 tests passed. A run concurrent with race
  compilation and SAST showed UI tests at 649–822ms. Investigated the affected
  Settings, App, and ManualSearch tests: they use mocked fetch, real DOM
  interactions, and `userEvent.setup({ delay: null })`, without artificial sleeps.
  An isolated full rerun passed with every individual test below 500ms. No suite
  time limit was applied and coverage was retained.
- `npm run build`: passed.
- `./scripts/sast.sh -j 1 --x-parmap`: full p/default scan passed, 352 applicable
  rules across 136 production targets, zero findings. Semgrep 1.185.0's default
  Eio worker crashed with SIGSEGV locally, including at one worker. Switching its
  worker implementation to parmap completed the same scan without exclusions.
- Impeccable detector: no findings. Headless Chrome render checked at 1280×900
  and 390×844; fixed the mobile navigation layout so search remains usable.
- New tests cover scoring/ranking, quality exclusions, permanent penalties,
  fallback ceiling, restart recovery, manual override and authorization, daily
  rechecks, cache retention/eviction, migration of both legacy request tables,
  mocked multi-source discovery, dispatch, and authenticated failure reporting.

Prowlarr owns download-client configuration. Completed files enter through the
existing staging monitor; downstream failures enter through the authenticated
`/api/acquisition/failure` integration documented in README. Callimachus cannot
infer remote transfer failures from Prowlarr search/grab responses alone.
