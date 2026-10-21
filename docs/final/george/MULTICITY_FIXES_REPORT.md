# Multicity fixes and integration report — 2026-10-21

## Scope and evidence

Functional scope remains G1–G5 of `03_GEORGE_MULTICITY_CATALOG.md`. The newer sync/fix/PR specification controls branch, migration sequence, Go version and Git operations. No live import, production access, deploy or PR merge was performed. Results below describe local tests, not production measurements.

Original base: `6d16d08a554d784e5741b6de86959cd1e201c49a`. Updated base: `1a7a8687b5a2e32b172d01d43c930599b902483b`. Branch: `final/multicity-catalog`; target: `dev`. Go changed with upstream from 1.26.0 to **1.26.10**; feature does not change go.mod/go.sum.

Before synchronization, all 38 changed/new feature files and binary patch were backed up with SHA-256 manifest outside the product in `codex_local/backups/multicity-before-sync-20261021`. Verification found no mismatches. Local checkpoint `b017b82` was rebased to `de30d66` on updated dev. Conflict resolutions retained Pavel's fixed Timepad clocks, public-plan/submission checks and all feature city tests. Local main/dev and `fix/backend-final-audit` at `73d9da031cb11477b36021f155670949cc613c1c` were preserved.

Evidence directory outside Git: `codex_local/multicity-fix-20261021/evidence`. Raw logs and disposable DB files are not PR artifacts. Existing independent review at `codex_local/reviews/FINAL_MULTICITY_REVIEW.md` records the earlier state; it is not used as proof that these fixes pass.

## Confirmed defects and corrections

| Area | Reproduction / earlier state | Correction and verification |
|---|---|---|
| Migration order | Local 26 was below already released 30 | Final tree contains only new `000031_multicity_city.sql`; no placeholders or allow-missing. M01–M10 below. |
| Test DB destination | URL parameters could override apparent host/database | Shared guard checks strict URL, effective pgx config and PG environment before connection. Negative tests perform no network access. |
| City/provider mapping | Known city UUID could use another city's location | Startup profiles validate DB name/timezone and provider identifiers; bad Moscow/Kazan combinations now fail before sync. Explicit additional profiles retain universal architecture. |
| Saturday report | Saturday morning selected the following week | Select current evening until 22:00 local, then next Saturday; boundary tests. |
| Venue identity | Invalid UUID could count as eligible/valid place | Shared nonzero UUID validation for candidate snapshot, completeness and samples; raw denominators retained. Regression includes malformed/nil IDs. |
| Generic timeout test | Nanosecond deadline relied on scheduler timing | Test transport observes context cancellation and asserts DeadlineExceeded. Production timeout unchanged. |
| Padded source mapping | Import canonicalized key but SQL mapping lock used exact raw key | Lock uses the same Unicode whitespace normalization. Space/tab/NBSP tests failed before fix, pass afterward. |
| Canonical source collision | Two raw keys collapsed into one provider identity | Serialized canonical uniqueness check on creation and fail-closed validation of legacy duplicates before execution. No automatic rewrite of existing keys. |
| System source | New dev owns user-submission source_kind=system | Existing generic-only Get/List retained; scheduling requires generic prefix. Regression verifies no user sync run or importer call. |
| Busy worker result | Lock skip looked like successful return | Explicit busy result and skipped log; next-tick retry tested with real PostgreSQL locks and two independent worker loops/pools. |

G3 remains limited to two demonstrated normalization defects: nonpositive event duration and reversed price bounds. The controlled malformed records are rejected; null/zero price semantics and existing Moscow identities remain unchanged. Their frequency in the real catalog is unknown.

## Migration matrix

Pavel reported production versions **25 and 30 already applied**. Those SQL files are unchanged. New 31 was checked against fetched team branches and open PRs. The new metadata migration is additive, transactional and locks cities during conflict checks. Down retains metadata and referenced catalog rows.

Tests use PostgreSQL **17.5**, a newly initialized loopback-only disposable cluster on port **65428**, database `multicity_fixture` and uniquely named `multicity_migration_*` databases. No shared database or historical local version-26 cluster is used. Matrix tests create/drop only their own temporary databases. Both UTF-8/C and ICU collation cases were exercised.

| ID | Scenario | Result |
|---|---|---|
| M01 | Empty DB → 1…25,30,31; CheckCurrent | PASS |
| M02 | dev schema 30 → only 31 applied | PASS |
| M03 | Repeated Up, no duplicate metadata | PASS |
| M04 | public_plan, submissions, user events and system source preserved | PASS |
| M05 | Existing compatible canonical city | PASS |
| M06 | Semantic city duplicate under different UUID | PASS: explicit rejection, no partial changes |
| M07 | Reserved UUID conflicts in name/timezone/coordinates; Latin duplicate | PASS: rollback |
| M08 | Uppercase Cyrillic conflict under C locale | PASS: rejection |
| M09 | Down31 with FK references and preserved version-30 data | PASS: data retained |
| M10 | Binary/schema compatibility boundary | PASS: new binary rejects30/accepts31; separately built actual dev binary accepts30/rejects31 |

The old-binary check was built from an isolated archive of exact dev SHA, not an emulated expected-version function. See `matrix-and-workers.log`, `old-dev-migrate.log`, `old-binary-boundaries.log` and full suite log.

## Checks and limitations

| Check | Status | Evidence / qualification |
|---|---|---|
| Go version in backend | PASS | go1.26.10 windows/amd64 |
| go build ./... | PASS | Full backend |
| go vet ./... | PASS | Full backend |
| go test -p 1 ./... -count=1 | PASS | Final run: 36 packages, 1134 passed test/subtest records, 0 failures; 5 test-level skips listed below. Both isolated DB environment variables set. |
| go test -race ./internal/providers -run '^TestMulticity' -count=1 | PASS | CGO/race available; `race-final.log` |
| Matrix M01–M10 | PASS | Actual disposable PostgreSQL, not SQL-only comparison |
| Generic, KudaGo, Timepad and city guards | PASS | Included in full suite; local mock providers |
| Canonical source collision / concurrent Create | PASS | `source-collision-red.log` → `source-collision-green.log`; legacy ambiguity blocked before importer |
| go test -race ./internal/eventsources -run '^TestCanonicalSource' -count=1 | PASS | Concurrent creation and legacy collision regressions; `source-collision-race.log` |
| Kazan importer → DB → PoolBuilder → room lifecycle | PASS | Synthetic 4 events / 4 candidates, hard-constraint violations0; Moscow/replay assertions |
| Fixed-as-of catalog baseline | PASS | `baseline-final.json`: all20, eligible14, unknown-price eligible4 |
| npm ci on exact dev copy | PASS | Product frontend/lockfile unchanged |
| npm run check, Node22.22.2 | PASS | 41 files / 261 tests, typecheck, lint and build |
| npm audit on exact dev copy | FAIL: 2 high advisories | Existing source-map-js and Vite dependencies; feature changes none. Owner decision required before Ready PR. |
| Pinned sqlc v1.29.0, twice | PASS | Isolated dev copy with31; no semantic changes against existing generated sources and no second-run drift |
| OpenAPI Go/TS generation | NOT RUN | No API/schema input changes; SQL generation checked separately |
| Playwright UI E2E | NOT RUN | No installed Playwright browser in this environment; optional gate, no live MAX claim |
| Live providers / production catalog / MAX UX | NOT RUN | Not authorized and explicitly excluded |

Five test-level skips in the complete suite are **not passes**: `TestGenericEventSourcesBrowserAgainstLiveHandler`, `TestEventSubmissionMigrationRoundTripAndPublishedDataGuard`, `TestKudaGoRealDataEndToEnd`, `TestProviderRealRoomEndToEnd`, `TestRealCatalogProductionPoolBuilder`. The separate submission rollback test requires its own opt-in migration DB; preservation across30→31 is covered by M04/M09. Packages with no tests are not counted as test failures or behavioral coverage.

Initial failed attempts retained in evidence: environment configuration for migration command (missing APP_ENV/LOG_LEVEL and invalid HTTP port0); PostgreSQL17 removed SHOW lc_collate/ctype usage in new test harness, replaced by database catalog metadata; generic local TLS fixtures blocked in sandbox before rerun with local-network permission; initial frontend policy check differed only in CRLF/LF, verified and regenerated solely in temporary copy; Node24.7 engine warning avoided by using CI-compatible Node22.22.2. None is presented as a successful check. Confirmed red regressions are recorded separately from these setup failures.

## Security finding owned by frontend

`npm audit` against the unchanged dev lockfile reports high findings in **source-map-js** ([GHSA-68fv-2mgg-jv7q](https://github.com/advisories/GHSA-68fv-2mgg-jv7q)) and **Vite** ([GHSA-587h-7wmj-h5pm](https://github.com/advisories/GHSA-587h-7wmj-h5pm), plus two moderate advisories grouped under Vite). This is a dependency-audit finding, not a demonstrated exploit of the multicity implementation. No dependency update was made in Pavel's module. The owner explicitly authorized **Draft PR only** on21.10, with dependency fixes assigned to Pavel in the PR blocking TODO. Do not promote to Ready until the security finding is resolved and checks rerun. Audit output is in `npm-audit.json` outside Git.

## Functional completion and rollout

1. **Implemented:** configurable city-aware ingestion, city/run identity guards, replay/reconciliation isolation, explicit Generic bindings, two proven Generic normalizer fixes, quality reporting and regression tests. Existing room/ranking logic is reused.
2. **Fixture/isolated only:** G1 Moscow baseline, G4 Kazan catalog and service lifecycle, importer concurrency and migration/data-preservation matrix. G1 live-audit objective is not claimed complete.
3. **Real catalog checks pending:** source/city coverage, last successful sync, representative10–20-card audit, duplicates across providers, actual normalization failure frequency, price/category/date distribution, URL/poster reachability and top24 quality.
4. **Second city NOT PRODUCTION-READY:** no authorized live KudaGo/Timepad response, no real eligible-event coverage, no production worker infrastructure, no MAX two-account UI validation. Long-running worker fairness is unproven; provider-wide locks are intentionally preserved.
5. **After merge:** coordinated schema/binary rollout; stop old ingestion writers before new sync, apply31 normally, retain Moscow worker, pass city/provider/binding env into backend/workers, validate live coverage before enabling Kazan worker/UI. Detailed commands/config contract are in HANDOFF/SOURCES; Compose, UI and deployment are unchanged here.

Rollback is disabling the second worker/city availability; do not delete catalog data or roll back25/30. CheckCurrent rejects mixed schema/binary generations, so database rollback and application artifact rollback require coordinated ownership. No automatic production rollback is proposed.

## Changed file groups

- `backend/cmd/catalog-quality/*`: read-only fixture diagnostics, baseline boundary tests, isolated Kazan PoC.
- `backend/cmd/event-sync/*`, `backend/internal/config/*`: startup profile/config validation, city-aware scheduling and worker-lock regressions.
- `backend/internal/providers/{kudago,timepad,generic}/*`: provider city/timezone transport and normalization, proven quality fixes and tests.
- `backend/internal/providers/repository.go`, `sourceconfig/*`, `backend/internal/eventsources/*`: persistence ownership, Generic city bindings/canonical identities, system-source isolation and regression tests.
- `backend/internal/testdbguard/*`: shared preconnection disposable-target guard and parser-only security tests.
- `backend/migrations/000031_multicity_city.sql`, `city_metadata_test.go`, `multicity_upgrade_test.go`, `embed_test.go`: new metadata and integration gates; SQL25/30 unchanged.
- `docs/final/george/*`: fixture provenance, SQL for owner-operated read-only measurements, source/config contract and handoff.

Final diff contains 52 files. Exact path list is recorded locally in `evidence/final-files.txt`; the PR Files changed view will provide the same manifest. Compared with origin/dev, SQL25/30, go.mod/go.sum, rooms, frontend and OpenAPI are unchanged. All changed Go files are gofmt-clean; git diff --check passes. No database dumps, real DSN/passwords, temporary review executables or Codex infrastructure are included. Parser-only security tests deliberately contain non-secret dummy DSN strings.

Repeated fetch immediately before preparation still returned dev `1a7a8687b5a2e32b172d01d43c930599b902483b`; no remote feature branch or competing31 migration/open PR was found. All other local branch heads exactly match the pre-sync backup. Independent follow-up static review closed both canonical-source and malformed-venue findings without additional actionable findings. This review is separate from the parent's executed DB/race tests.
