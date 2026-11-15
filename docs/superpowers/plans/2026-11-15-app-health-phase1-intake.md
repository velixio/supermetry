# App Health Phase 1: Intake Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Late events stop disappearing silently: every one is counted as a `too_old` drop that site owners see in Health, and app developers get documented client rules (UA, hosts, privacy mode).

**Architecture:** Add a `too_old` reason to `internal/dropstats`, record it at both late-drop sites in `internal/ingest/handler.go`, widen the `site_drop_daily` CHECK constraint, expose the reason in the OpenAPI enum and the Health UI labels, and write a short client guide.

**Tech Stack:** Go, pgx, Postgres migrations, OpenAPI + openapi-typescript, React (Bun).

**Spec:** `docs/superpowers/specs/2026-11-15-app-health-design.md` (§4, Delivery plan item 1)

## Deviation from the spec (decided by Claude, 2026-11-15)

The spec's Phase 1 also lists a 7-day late window for `diagnostic_report`. On
`/api/public/v1/events` an event needs the daily visitor salt for its day, and
salts are deleted after about 48 h (`handler.go:296-303`), so 7-day-old events cannot be stored on this
path without breaking the salt rule. The 7-day window moves to Phase 3, where
the mapped event goes to `app_events`, which hashes with the per-app HMAC instead of daily salts.
Phase 1 makes the loss visible instead.

## Global Constraints

- Go binary + Postgres only; no DB read on the ingest hot path.
- Forward-only transactional migrations; next number is `0252`.
- Drop reason names are persisted text; append new reasons, never reorder `dropstats.Reason` iota values.
- Frontend uses Bun (`bun run gen:api`), never npm.
- No VoiceStudio names in core code outside the Phase 3 mapping file (docs may use it as an example).
- Commits: identity velixio, no trailers, no AI mentions; scan staged diff for emails.

## Review Focus

1. Event with `ts` exactly 24 h old: still accepted (boundary is `Before(now-24h)`); pin it in Task 1.
2. Late event on a site in a far time zone (e.g. `Pacific/Kiritimati`, +14): the drop is booked on the receive day in the site's zone, not the event's day (the old day may already be closed/retained away). Pin in Task 1.
3. Missing-salt late path (`handler.go:296-303`, event between ~24 h and salt expiry on a different local day): also records `too_old`.
4. Health UI receiving an unknown future reason must not crash: `DroppedTrafficCard` falls back to the raw reason name. Pin in Task 3.
5. Migration on a DB that already has rows: CHECK swap must not fail on existing data (only widening).

---

### Task 1: `too_old` drop reason, recorded on late drops

**Files:**
- Modify: `internal/dropstats/recorder.go:31-49` (append `TooOld` before `reasonCount`, name `"too_old"`)
- Modify: `internal/ingest/handler.go:180-185` and `:296-303`
- Create: `internal/db/migrations/0252_drop_reason_too_old.sql`
- Test: `internal/dropstats/recorder_test.go`, `internal/ingest/diagnostics_compat_test.go`

**Interfaces:**
- Produces: `dropstats.TooOld Reason` with `String() == "too_old"`; Phase 3 reuses it for `app_events` drops.

- [ ] **Step 1: Write failing tests**
  - `recorder_test.go`: `TestReasonTooOldName`: `dropstats.TooOld.String() == "too_old"` and `parseReason("too_old")` round-trips (use the exported path the existing tests use for names).
  - `diagnostics_compat_test.go`: extend `TestDiagnosticsCompatOldTimestamps` so for ages `-3*24h` and `-7*24h` the site's drop stats for the receive day contain `too_old` = 1 and no event row is stored; for `-23h` and exactly `-24h`, the event is stored and `too_old` is 0. Read drop stats the way existing handler tests do (flush the recorder, then query `site_drop_daily`).
  - Add `TestDiagnosticsCompatLateDropUsesReceiveDayInSiteTZ`: site TZ `Pacific/Kiritimati`, `fakeNow()` receive time, event 3 days old → `site_drop_daily.day` equals `salts.LocalDay(fakeNow(), "Pacific/Kiritimati")`.
- [ ] **Step 2: Run, expect FAIL** (`undefined: dropstats.TooOld`)
  `SUPERMETRY_TEST_DSN='postgres://supermetry:supermetry@127.0.0.1:5433/supermetry_test?sslmode=disable' go test ./internal/dropstats ./internal/ingest -run 'TooOld|DiagnosticsCompat' -count=1`
- [ ] **Step 3: Implement**
  - Append `TooOld` and `"too_old"`.
  - At both late-drop sites add `h.recordDrop(site, dropstats.TooOld, "", receivedAt)` next to `health.IngestLateDropped.Add(1)` (pass `receivedAt` so the day is the receive day, never the old event day).
  - Migration `0252`: drop and re-add the `site_drop_daily` reason CHECK with the 10 existing names plus `'too_old'` (find the constraint name with `\d site_drop_daily` in the test DB; Postgres auto-names it `site_drop_daily_reason_check`).
- [ ] **Step 4: Run, expect PASS**, then the whole package: `go test ./internal/dropstats ./internal/ingest ./internal/db/... -count=1`
- [ ] **Step 5: Commit** in two commits: `dropstats: add too_old drop reason` (recorder + migration + test), `ingest: count late events as too_old drops` (handler + compat test).

### Task 2: API enum and generated clients

**Files:**
- Modify: `internal/api/openapi.yaml:6634` (append `too_old`)
- Regenerate: `web/src/lib/api.gen.ts`, `web/packages/api-client/src/schema.ts`

- [ ] **Step 1:** Append `too_old` to the enum. If an API test validates drop-reason responses against the spec (`grep -rn 'clock_skew' internal/api/*_test.go`), add a case returning `too_old`; run it and see it fail first.
- [ ] **Step 2:** `cd web && bun run gen:api`; `git diff --stat` shows only the two generated files plus the yaml.
- [ ] **Step 3:** `go test ./internal/api -count=1` (with the DSN) passes.
- [ ] **Step 4: Commit** `api: expose too_old drop reason`.

### Task 3: Health UI labels

**Files:**
- Modify: `web/src/features/health/DroppedTrafficCard.tsx:18` (label `too_old: "Too old"`)
- Modify: `web/src/features/health/HealthPage.tsx:179` (explanation `too_old: "the event's timestamp was more than 24 hours old (queued offline too long)."`)
- Test: the nearest existing test for these components (`ls web/src/features/health/*.test.*`); if none, add `DroppedTrafficCard.test.tsx` in the repo's existing test style.

- [ ] **Step 1:** Failing test: card renders "Too old" for reason `too_old`, and renders the raw name for an unknown reason string (cast) without throwing.
- [ ] **Step 2:** Run `cd web && bun run test -- DroppedTrafficCard` (use the script name from `web/package.json`); expect FAIL.
- [ ] **Step 3:** Add labels; add the raw-name fallback if missing (the typed maps make `too_old` required once Task 2 regenerated types).
- [ ] **Step 4:** Test passes; `bun run typecheck` passes.
- [ ] **Step 5: Commit** `health: label too_old drops`.

### Task 4: Client guide for apps

**Files:**
- Create: `docs/guide/apps.md`

Content (short, plain, ≤120 lines):
- Send a custom User-Agent `AppName/version (platform; arch)`; Electron's default UA and HTTP library defaults (curl, Go, Python-requests, empty) are filtered as bots.
- Add the app's reporting host to the site's allowed hosts, else 400.
- Anonymous tracking mode or profiles off strips props; diagnostic reports need profiles on until App Health ships.
- Events older than 24 h are dropped and show as "Too old" in Health; until the app intake lands (App Health phase 3), flush queued reports promptly.
- Use a stable `id` per report for dedup.
- Example payload (generic, `diagnostic_report` with a few props).

- [ ] **Step 1:** Write it; link it from `docs/guide/` index if one exists (`grep -rn 'ad-blockers.md' docs | head`).
- [ ] **Step 2:** Scan for emails; commit `docs: client guide for desktop and mobile apps`.

### Finish

- [ ] `go build ./... && go vet ./...`, full `go test ./internal/... -count=1` with the DSN, `cd web && bun run build`.
- [ ] Push branch `feat/app-health-p1-intake`, open PR. Phase 1 has no `/sites/new` change (no new kind yet); the instance restart waits for Phase 2.
