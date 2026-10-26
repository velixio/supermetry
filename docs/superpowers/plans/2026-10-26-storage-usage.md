# Organization and Site Storage Usage Implementation Plan

> **For agentic workers:** use `test-driven-development` for each behavior change and `verification-before-completion` before each commit. Work in the listed order; do not implement billing or tenant-facing visibility.

**Goal:** give instance administrators a fresh, honest storage estimate per site and organization without scanning tenant data in requests or slowing ingestion.

**Architecture:** a resumable background reconciler scans PostgreSQL sources partition-by-partition and replay blobs by prefix into generation-scoped staging rows. It atomically publishes per-site snapshots only after a complete run. Admin APIs read snapshots; organization totals are derived from sites.

**Tech Stack:** Go, PostgreSQL/pgx, the existing in-process scheduler and blob stores, OpenAPI, React/TypeScript, TanStack Query, shadcn/Tailwind, Bun.

**Spec:** `docs/superpowers/specs/2026-10-26-storage-usage-design.md`

## Constraints and decisions

- The value is an operational estimate, not a billable physical-byte measurement.
- All site-owned PostgreSQL rows are in scope; shared/instance overhead is explicitly excluded.
- Replay recordings and CSS are measured from blob storage, separately from database rows.
- Admin only in v1. No quotas, enforcement, history chart or tenant settings UI.
- Publish complete generations atomically; never show partial totals.
- Keep external I/O outside transactions and add no reads to ingestion.
- Use migration number `0245`; confirm it is still free immediately before implementation.
- Plan for 6–8 logical commits: schema, database scanner, replay scanner, orchestration, API, UI, and final documentation/tests.

## Task 1: Add the reconciliation schema and model

**Files:**

- Create: `internal/db/migrations/0245_site_storage_usage.sql`
- Create: `internal/storageusage/model.go`
- Create: `internal/storageusage/store.go`
- Create: `internal/storageusage/store_test.go`
- Modify: migration manifest/test files discovered by `rg "0244|migrations" internal/db`

**Steps:**

1. Write failing migration/store tests for nonnegative `bigint` values, one active/resumable run, its captured site set, idempotent stage replacement, indexed site/run cleanup, and snapshot removal on erasure.
2. Add `storage_usage_runs`, `storage_usage_run_sites`, `storage_usage_stage`, and `site_storage_usage` exactly as specified. Use `timestamptz`, named checks, indexed foreign keys and forward-only SQL.
3. Implement narrow store operations: create/resume a run, replace one source's staged contributions, advance its cursor, load state, atomically publish, and abandon a run when the measurement version changes.
4. Test that publishing fills zero snapshots only for captured, still-unerased sites with no usage, leaves sites created mid-run unknown, ignores sites erased mid-run, uses one timestamp, and rolls back entirely on failure.
5. Run `go test ./internal/storageusage -run 'Test(Store|Publish|Migration)' -count=1` and the repository migration tests.
6. Commit: `feat(storage): add usage reconciliation schema`.

## Task 2: Build the PostgreSQL logical-byte scanner

**Files:**

- Create: `internal/storageusage/sources.go`
- Create: `internal/storageusage/postgres.go`
- Create: `internal/storageusage/postgres_test.go`
- Modify: `internal/storageusage/model.go`
- Modify: `internal/db/partitions.go` only if a reusable read-only leaf-discovery helper is needed

**Steps:**

1. Write a table-driven source registry covering `hot`, `retained`, and `auxiliary` site-owned tables. Mark deliberate exclusions with a reason.
2. Add a catalog contract test that discovers tables containing `site_id` and fails when one is neither measured nor excluded.
3. Write failing fixtures for multiple sites, every retention class, partitioned and unpartitioned sources, imported data, large/TOAST-able values, an empty site, and byte totals over 32-bit range.
4. Implement safe source discovery with quoted identifiers from the fixed registry. Process one leaf relation or bounded key range per call, aggregate estimator-v1 bytes with `sum(pg_column_size(row_alias))` by `site_id`, and set a local statement timeout. Do not separately charge indexes, pages or TOAST relations.
5. Replace staging contributions by stable source key so retrying a scan is idempotent. Never interpolate user input into relation names.
6. Add an `EXPLAIN`/query-shape test proving each work unit is one bounded relation and admin paths are not involved.
7. Run `go test ./internal/storageusage -run 'Test(Postgres|SourceRegistry|Catalog)' -count=1`.
8. Commit: `feat(storage): measure site database usage`.

## Task 3: Measure replay recordings and CSS from the blob store

**Files:**

- Create: `internal/storageusage/replay.go`
- Create: `internal/storageusage/replay_test.go`
- Modify: `internal/replay/blob.go`
- Modify: `internal/replay/blob_disk.go`
- Modify: `internal/replay/blob_s3.go`
- Modify: replay blob-store contract tests found by `rg "BlobStore|List\(" internal/replay --glob '*_test.go'`

**Steps:**

1. Write failing contract tests for paginated/bounded prefix listing, returned object sizes, recording/CSS classification, duplicate CSS counted once, missing objects, and cancellation.
2. Add the smallest shared listing primitive needed by both disk and S3 stores. Preserve existing callers and do not perform database work in blob implementations.
3. Implement a replay scan that stages recording and CSS totals per site from `replay/{site}/...` keys. Treat the blob store—not `replay_sessions.bytes`—as reconciliation authority.
4. Test drift repair where replay index bytes are absent or stale, and test totals above 32-bit range and overflow rejection.
5. Ensure external listing completes before the short staging transaction begins.
6. Run `go test ./internal/replay ./internal/storageusage -run 'Test.*(Blob|Replay|CSS)' -count=1`.
7. Commit: `feat(storage): reconcile replay blob usage`.

## Task 4: Orchestrate bounded, resumable reconciliation

**Files:**

- Create: `internal/storageusage/reconciler.go`
- Create: `internal/storageusage/reconciler_test.go`
- Modify: `cmd/supermetry/serve.go`
- Modify: `cmd/supermetry/serve_test.go` or the nearest scheduler registration test
- Modify: `internal/health/health.go`
- Modify: `internal/health/health_test.go`
- Modify: `internal/retention/retention.go`
- Modify: site-erasure code in `cmd/supermetry/serve.go` or its extracted owner

**Steps:**

1. Write failing state-machine tests: capture the site set at start, start only when due, one bounded source per work unit, resume after cancellation/restart, retry without double counting, discard a version-mismatched run, tolerate concurrent ingestion/retention as a best-effort scan window, and atomically publish only after all sources finish.
2. Implement `Reconciler.Step(ctx, now)` and a scheduler job using the existing advisory-lock mechanism. Run frequently to make progress, but begin a new generation only when the last complete one is at least 24 hours old or none exists.
3. Put hard time/work bounds on each step. Never keep a transaction open during a table scan or blob listing.
4. Integrate erasure so completing site erasure immediately removes its snapshot/staging rows; verify restorable sites remain counted until then.
5. Add process-level health counters/gauges for last success, failures, run duration/status and pending sources, without tenant labels and without work in health handlers.
6. Test two scheduler instances, advisory-lock exclusion, a dropped raw partition between steps, replay-store unavailability, and recovery while retaining the previous snapshot.
7. Run `go test ./internal/storageusage ./internal/health ./internal/retention ./cmd/supermetry -run 'Test.*(Reconcile|Storage|Scheduled|Erase)' -count=1`.
8. Commit: `feat(storage): schedule atomic usage reconciliation`.

## Task 5: Expose snapshots through the admin API

**Files:**

- Modify: `internal/platform/admin/queries.go`
- Modify: `internal/platform/admin/admin_test.go`
- Modify: `internal/platform/http/handlers_admin.go`
- Modify: `internal/platform/http/admin_test.go`
- Modify: `internal/api/openapi.yaml`
- Regenerate: generated Go API types and `web/src/api/generated/*` via the repository generator

**Steps:**

1. Write failing service tests for site snapshots, exact organization sum, a missing site snapshot producing an unknown organization value, soft-deleted sites included, erased sites excluded, and cross-organization isolation.
2. Add one nullable `StorageUsage` model to admin site/organization responses. Carry the category breakdown on each `AdminSite` response, including sites embedded in organization detail; do not add a site-detail route.
3. Join `site_storage_usage` once in `ListSites`; aggregate snapshots directly in organization list/detail queries. Do not derive totals from the detail's capped site slice.
4. Update OpenAPI and regenerate both clients. Add/update authorization and OpenAPI drift tests.
5. Add a stable query-shape/behavior test showing the new storage fields come only from `site_storage_usage` and never invoke a live size/blob calculation. Preserve the pre-existing lifetime-pageview aggregate.
6. Run `go test ./internal/platform/admin ./internal/platform/http ./internal/api -run 'Test.*(Storage|ListOrganizations|ListSites|OpenAPI|Authorization)' -count=1`.
7. Commit: `feat(admin): expose organization storage usage`.

## Task 6: Add storage to the admin UI

**Files:**

- Modify: `web/src/features/admin/AdminSitesPage.tsx`
- Modify: `web/src/features/admin/AdminOrganizationsPage.tsx`
- Modify: `web/src/features/admin/AdminOrgsSites.test.tsx`
- Modify: `web/src/lib/format.ts`
- Modify: `web/src/lib/format.test.ts`
- Modify: locale files under `web/src/messages/locales/`

**Steps:**

1. Write failing formatter tests for bytes at zero, KiB/MiB/GiB boundaries and large values; write UI tests for `Calculating…`, total/breakdown, freshness and deleted organizations.
2. Implement locale-aware IEC byte formatting with sensible precision.
3. Add Storage to admin site and organization tables and per-site organization detail. Use a compact tooltip/popover for database and replay values plus the last-calculated time.
4. Ensure null remains `Calculating…`; never coerce it to `0 B`. Keep tables usable at mobile widths and preserve accessible labels.
5. Add English, Spanish, German and Bengali strings and run locale parity tests.
6. Run `cd web && bun run test -- AdminOrgsSites format && bun run typecheck && bun run build`.
7. Commit: `feat(admin): display estimated storage usage`.

## Task 7: Prove scale, failure behavior, and documentation

**Files:**

- Create or modify: `tools/bench/storage_usage_*`
- Modify: `docs/features.md`
- Modify: `.workspace/memory.md`
- Modify: `.workspace/progress.md`
- Modify: relevant operator documentation discovered by `rg "retention|storage|replay" docs README.md`

**Steps:**

1. Add a repeatable fixture/benchmark with many sites and representative hot, retained, auxiliary and replay data. Record per-step duration, rows/sources scanned and peak staging rows.
2. Verify that work is bounded, cancellation is prompt, a failed generation preserves the last snapshot, and no admin request initiates a measurement.
3. Document inclusion/exclusion semantics, 24-hour freshness, estimator versioning, and why values are unsuitable for billing.
4. Update the feature list and durable workspace decision after the behavior is verified.
5. Run final gates:

   ```bash
   go test -count=1 ./...
   go vet ./...
   cd web
   bun run test
   bun run typecheck
   bun run build
   ```

6. Review `git diff`, generated-file drift, migration ordering, secrets, email addresses and commit identity. Split any oversized batch while keeping every intermediate commit buildable.
7. Commit: `test(storage): verify reconciliation and document estimates`.

## Release gate

- The last complete generation appears in Admin Sites and Admin Organizations with matching sums.
- A killed mid-run process resumes without double counting and does not publish partial data.
- Replay recording and CSS values agree with disk and S3 fixture listings even when the replay index is wrong.
- A newly introduced `site_id` table fails the source-registry contract until classified.
- Soft-deleted storage remains visible until the erasure worker finishes; then it becomes zero/absent.
- Query/tests confirm reading storage never launches an analytics-size or blob scan; the existing lifetime-pageview aggregate remains unchanged.
- All commands above pass, and the final plan/spec commit contains no private email or secret.
