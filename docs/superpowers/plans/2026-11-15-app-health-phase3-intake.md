# App Health Phase 3 plan: app intake, mapping, storage, issues

Brief: `2026-11-15-app-health-phase3-brief.md` (binding). Spec: `../specs/2026-11-15-app-health-design.md`.
Every task is test-first: write the failing test, see it fail, implement, see it pass.

## Task 1: migrations 0255-0259 (+ partition, retention, erase plumbing)
- 0255 `app_events` (partitioned by month on `ts`, BRIN on `ts`, `(site_id, issue_id, ts)` btree, kind CHECK).
- 0256 `app_issues`. 0257 `app_hash_secrets`. 0258 drop reasons CHECK (`app_invalid`, `app_duplicate`, `app_rate_limited`).
  0259 `app_link` is a settings key, so no DDL: reserved for the web-site link index if a test needs one.
- Files: `internal/db/migrations/*.sql`, `internal/db/partitions.go` (timestamp monthly partitions for `app_events`),
  `internal/retention/retention.go` (drop `app_events` partitions, erase tables), `internal/storageusage/sources.go`,
  `internal/dropstats/recorder.go`.
- Tests: `internal/db/migrate_app_events_test.go` (kind CHECK, partitions created, cascade on site delete),
  `internal/retention/app_events_test.go` (old partition dropped), `internal/dropstats/recorder_test.go` (names, Reasons()).

## Task 2: appingest core, pure functions
- `internal/appingest/scrub.go`: `ScrubFrames`, `Fingerprint`.
- `internal/appingest/event.go`: `Parse` (strict per-event validation, field limits, enums).
- Tests: `TestScrubFrames*`, `TestFingerprint*`, `TestParseEvent*` with hand-computed values.

## Task 3: intake core (no HTTP)
- `internal/appingest/intake.go`: dedup TTL cache, per-key budget, tag allowlist/cardinality, environments,
  timestamps, bounded channel.
- Tests: `TestIntakeDrops`, `TestIntakeTagsStripped`, `TestIntakeTagCardinality`, `TestIntakeQueueFull`.

## Task 4: writer
- `internal/appingest/writer.go`: flush on 500/1s/shutdown; secrets, issue upsert, regression, CopyFrom.
- Tests (real Postgres): `TestWriterCreatesAndIncrementsIssue`, `TestWriterOutOfOrder`, `TestWriterRegression`,
  `TestWriterHashesIDs`, `TestWriterFlushOnShutdown`.

## Task 5: endpoint
- `internal/appingest/handler.go`; route + OPTIONS + OpenAPI; registry `HashSecret`; wiring in `cmd/supermetry`.
- Tests: `TestHandlerRequestChecks` (400/401/429), `TestHandlerDropsInReplyAndDropstats`, `TestHandlerCORS`,
  `TestHandlerLoad` (20 events at 16 KB ok, 16 KB + 1 gives 400).

## Task 6: VoiceStudio mapping
- `internal/appingest/diagnostic_map.go`; `ingest.Handler.AppSink`; `sites.Site.LinkedAppID`; registry load.
- Tests: `TestMapDiagnostic*`; ingest tests for a linked site (3-day-old report stored), unlinked unchanged.

## Task 7: link API
- `PUT /api/private/sites/{site}/app-link`, OpenAPI, store. Tests: owner ok, other org 400, non-app 400, clear.

## Task 8: UI
- Settings select "Send diagnostic reports to app"; labels for new drop reasons. Tests with vitest.

## Task 9: docs
- `docs/guide/apps.md` endpoint section; CHANGELOG line.
