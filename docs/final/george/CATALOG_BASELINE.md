# G1 — isolated fixture baseline

Measured on 2026-10-20, reporting timezone Europe/Moscow (UTC+03:00).
Base: `6d16d08a554d784e5741b6de86959cd1e201c49a`.
Branch: `final/multicity-catalog`.

This is **not a measurement of the live Moscow catalog**. The owner explicitly
selected existing local fixtures and isolated derived test data. No production
database, provider token, live synchronization or real user data was used.
SQL provenance for an owner-operated snapshot measurement is in
`catalog-readonly.sql`; fixture reports identify fixture provenance instead.

## Before changing ingestion

Go 1.26.0 was available from `backend`. The default Windows Go build cache was
inaccessible in the sandbox; an external workspace cache was used without
changing project dependencies.

```powershell
$env:GOCACHE = 'C:\study\ХАКАТОН МАКС осень 2026\codex_local\cache\go-build-multicity'
go test ./internal/providers/timepad -run '^(TestImportStopsWhenPageTotalIsReached|TestImportCancellationStopsPaginationWait)$' -count=1
go test ./internal/providers/generic -run '^TestNormalizeRejectsContradictoryQualityFields$' -count=1
```

Initial results:

| Check | Before | Evidence |
|---|---|---|
| Timepad page-total test | FAIL | Fixed 2026-10-01 event is past relative to `time.Now`; no upsertable events |
| Timepad cancellation test | FAIL | Two fixture records rejected as BeforeWindow, zero inserted |
| Generic end before start | FAIL | Contradictory record accepted |
| Generic end equals start | FAIL | Zero-duration record accepted |
| Generic price maximum below minimum | FAIL | Contradictory price range accepted |
| Generic latitude >90 / longitude <-180 | PASS | Existing validation rejects both; no fix needed |

The two Generic defects, each reproduced with a controlled record derived from
the existing `testConfig`, are the G3 scope. No claim about their prevalence in
the real catalog can be made.

## Static findings and limits

- Built-in scheduled sync selects `catalogseed.MoscowCityID`; Timepad also
  filters names to Moscow and both built-in normalizers hardcode its timezone.
- Generic manual and scheduled runners currently share one Moscow city.
  Configurable field mappings alone do not establish city ownership.
- Event identity is `(source, external_id)`; provider venue IDs omit city and
  the existing upsert can update `venues.city_id`. Cross-city identity reuse
  needs explicit rejection, not silent movement or rekeying historical events.
- Existing stale reconciliation is scoped by provider and city. Failed runs
  and upsert-only Generic/limited Timepad runs do not reconcile missing events.
- KudaGo sessions are distinct external IDs `event:start`; the existing ranker
  collapses series. Raw eligible counts and returned pool size are different.
- Categories are preferences in the current PoolBuilder, not a hard category
  gate. Unknown prices are not equivalent to zero; a budget scenario must
  report unknown-price coverage separately.
- Timepad uses short description as the available description; it preserves
  unknown prices. Poster presence in a fixture does not prove image
  reachability or correct MAX rendering.
- No real last-success timestamps, freshness lag, coverage percentages,
  live ticket availability, sample defect prevalence, duplicate rate or
  Moscow before/after production comparison is available. These are **N/D**,
  not zero. Raw source payload errors must not be included in public reports.

## Fixture measurements

Provenance: `make-baseline.ps1` derives 20 controlled variants from the two
existing `backend/seed/submission.json` cards. It writes `baseline.fixture.json`.
The source label is `submission-fixture`, not KudaGo/Timepad/Generic live data.
No writes to a database occur in either the generator or diagnostic CLI.

From `backend`, run:

```powershell
& ../docs/final/george/make-baseline.ps1
go run ./cmd/catalog-quality --fixture ../docs/final/george/baseline.fixture.json --city a0f625ee-2154-5a45-8afe-37adf955ec24 --as-of 2026-10-20T09:00:00Z --days 14 --revision 6d16d08a554d784e5741b6de86959cd1e201c49a
```

The fixed cutoff is 12:00 Moscow time on October 20; the period ends at
2026-11-03T09:00:00Z. The scenario selects October 24, evening, budget 300,000
kopecks, concerts/standup preferences, two distinct participants. `revision`
identifies the base commit; implementation changes are uncommitted.

| Metric / JSON provenance | Before | After |
|---|---:|---:|
| sources.submission-fixture.all | 20 | 20 |
| future within 14 days | 19 | 19 |
| published / provider_active / ticket_available / valid_ticket_url (each) | 19 | 19 |
| future_published_active_ticket_url (combined) | 16 | 16 |
| completeness.card_poster | 16/20 | 16/20 |
| completeness.known_price | 16/20 | 16/20 |
| completeness.valid_coordinates | 18/20 | 18/20 |
| completeness.description_complete | 18/20 | 18/20 |
| completeness.canonical_category | 19/20 | 19/20 |
| completeness.valid_time_place | 20/20 | 20/20 |
| scenario.eligible_before_ranking | 14 | 14 |
| scenario.candidates, maximum 24 | 14 | 14 |
| scenario.unknown_price_eligible | 4 | 4 |
| scenario.category_matched (returned candidates) | 13 | 13 |
| scenario.candidate_bucket / is_small | 10+ / false | 10+ / false |

The numbers come from `baseline.before.json` and `baseline.after.json`, not SQL.
The final CLI replaces the ambiguous `truncated` flag with `cap_reached`.
Neither flag is true in this corpus. Other numeric metrics are unchanged.
Singleton PoolBuilder calls measure eligible occurrences before ranking/series
collapse; one whole-catalog call measures the returned pool. Categories are soft
preferences, so category_matched can be smaller than candidates. Unknown prices
remain unknown even when the current core allows them through a budget filter.

The JSON contains 14 opaque sample IDs with controlled defects: four missing
posters, four unknown prices, two bad coordinates, two missing descriptions,
one invalid ticket URL, one unknown category. Only the first reason per card
is emitted. These are deliberately injected cases, not a representative live
sample. The image metric checks presence of the `card` role; URL checks are
syntactic, not allowlist or live availability checks. The time/place metric
checks a parseable start and nonempty venue ID, not a live venue/address audit.

No natural-key duplicate groups are present in this fixture. No KudaGo series
are represented. Cross-provider duplicates are **N/D** because the diagnostic
fixture has no trustworthy common title/place identity. Empty duplicate sample
arrays do not establish zero real duplicates. `sync: {}` means unobserved;
there are no fixture sync runs to infer last success, failure or freshness.

## G3 before/after

`internal/providers/generic/quality_regression_test.go` supplies a separate raw
normalizer corpus using the existing `testConfig`:

| Controlled record | Before | After |
|---|---|---|
| end before start | accepted | rejected |
| end equals start | accepted | rejected |
| maximum price below minimum | accepted | rejected |
| latitude >90 / longitude <-180 | rejected | rejected |
| unknown price / explicit free | null / zero | null / zero |

Three malformed records across two defect classes now fail normalization.
No existing catalog rows are rewritten. The normalized baseline fixture is
unchanged, so its quality aggregates remain comparable. Real affected-card
counts and whether these defects are among the most common are **N/D**.

## Owner-operated measurement still needed

`catalog-readonly.sql` uses a repeatable-read, read-only transaction, bounded
statement/lock timeouts and ends with ROLLBACK. Q1 provides source/city coverage;
Q2 latest attempt/completion per outcome without raw provider error strings;
Q3 natural-key duplicate counts; Q4 city identity preflight. It was syntax- and
schema-checked against the isolated fixture DB only.

Real catalog counts, sync freshness and sanitized failure reasons, series and
cross-provider review samples, image/link reachability, real filtered eligible
and top-24 counts, and a representative 10–20-card audit require an approved
snapshot or anonymized export. Q1–Q4 alone do not prove all these properties.
The CLI intentionally rejects database mode rather than presenting the
active-only `catalog.LoadCity` view as total raw catalog coverage. Do not connect
this CLI or the tests to production. Obtain a separate reviewed export workflow
if a live-data baseline is needed later.

## Fixes after dev sync — 2026-10-21
Current base1a7a8687b5a2e32b172d01d43c930599b902483b, Go1.26.10. The earlier Go1.26.0 run above is historical provenance. CLI now selects the current Saturday evening until22:00 local time and excludes malformed/zero venue identities from eligibility without changing raw denominators. Fixed Tuesday as-of and the20-card baseline remain the same; this is not a real-catalog improvement measurement. Current check results are in MULTICITY_FIXES_REPORT.md. Production metrics, URL reachability and live Kazan coverage remain unverified.
