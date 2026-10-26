# Organization and site storage usage: design

Status: accepted for planning, 2026-10-26. The first release is an admin-only operational estimate, not a billing meter or quota enforcement system.

## Goal

Show an instance administrator how much managed storage each site uses and aggregate those values by organization, without adding expensive scans to an HTTP request or the ingestion path.

The UI reports two clearly separated values:

- **Estimated database storage**: logical bytes in PostgreSQL rows attributable to a site.
- **Replay storage**: bytes occupied by that site's recording and CSS blobs.

Their sum is **Estimated storage**. Organization usage is always the sum of its sites; it is never maintained as a second counter.

## Non-goals

- Exact physical PostgreSQL allocation per tenant. Shared partitions, indexes, TOAST, free space, bloat and shared dictionaries cannot be attributed exactly.
- Billing, plan enforcement, automatic deletion or storage quotas. Event volume remains the primary commercial limit; storage is informational in this release.
- A tenant-facing storage page, storage history or growth charts. These can be added after the estimate has been operated and calibrated.
- Per-request or ingestion-hot-path database reads.

## Semantics

### Included

Database usage includes logical row bytes for every table whose data is owned by a `site_id`, grouped internally as:

1. `hot`: raw `events` and `sessions` rows across every stamped retention class.
2. `retained`: closed sessions/custom events, verified pages, cubes and daily/monthly/block rollups.
3. `auxiliary`: heatmaps, profiles, minute counters, imports, Search Console, drop statistics, link/pixel aggregates and other site-owned data tables.

Replay usage includes both recording blobs and deduplicated CSS blobs. A CSS blob belongs to the site in its blob key and is charged once, regardless of how many recordings reference it.

Shared dictionaries, database catalog overhead, WAL, backups and instance-wide configuration are excluded. The UI help text states this boundary.

### Deleted and special sites

- Active, demo and soft-deleted-but-restorable sites are measured because their data still occupies storage.
- A deleted organization's total continues to include its un-erased sites during the restore grace period.
- When site erasure completes, its published usage becomes zero and its staging rows are removed. The retained site tombstone does not keep stale usage.
- Changing a site's retention class does not reclassify old raw rows: all physical retention partitions are scanned.

### Freshness and unknown values

No snapshot means **Calculating…**, not zero. A successful full reconciliation publishes one common `measured_at` timestamp for all sites so organization totals never mix generations. A failed or interrupted run leaves the previous complete snapshot visible and marked with its existing timestamp.

The target is one complete reconciliation every 24 hours. Values are displayed in IEC units (KiB, MiB, GiB) without false byte-level precision.

## Data model

Migration `0245_site_storage_usage.sql` adds:

- `storage_usage_runs`: one resumable reconciliation generation, including status, cursor, timestamps, estimator version and last error.
- `storage_usage_run_sites`: the un-erased site IDs captured when a generation starts. Sites created later remain **Calculating…** until the next generation rather than receiving a false zero.
- `storage_usage_stage`: one idempotent contribution per `(run_id, source_key, site_id, category)`. Retrying a source replaces its contribution instead of adding it twice.
- `site_storage_usage`: one published row per site with nonnegative `hot_bytes`, `retained_bytes`, `auxiliary_bytes`, `replay_recording_bytes`, `replay_css_bytes`, `database_bytes`, `replay_bytes`, `estimated_storage_bytes`, `measured_at` and `measurement_version`.

Byte columns use `bigint` with nonnegative checks. `site_id` is indexed and references `sites(id)`. The stage table has an index supporting run cleanup and aggregation. Organization totals are queried from `site_storage_usage JOIN sites`; no organization snapshot table is added.

## Measurement pipeline

An `internal/storageusage` package owns source discovery, scanning, staging and publication. The HTTP/admin packages only read published snapshots.

### PostgreSQL sources

The source registry explicitly lists every site-owned table, its category and whether it is partitioned. A catalog contract test fails when a new `site_id` table is introduced without being classified as measured or deliberately excluded.

The reconciler processes one bounded source at a time:

- A partitioned table is scanned one leaf partition at a time and grouped by `site_id`. A leaf larger than 65,536 heap blocks (512 MiB at the default 8 KiB block size) is split at discovery into consecutive heap block ranges of that size, each scanned with a TID range predicate (`ctid >= '(from,0)' and ctid < '(to,0)'`, a `Tid Range Scan` that reads only the unit's blocks). Site-id ranges cannot bound the work because `events` has no index leading with `site_id`, so every range would still read the whole heap. The last range is open-ended so rows appended after discovery are counted. Unit keys carry the zero-padded block range, so cursor order stays deterministic; the block budget is part of the work fingerprint, so changing it starts a clean generation. Each unit therefore fits the 30 s statement timeout regardless of partition size.
- A small unpartitioned table is scanned as one source; a large one uses key-range batches.
- Estimator version 1 is exactly `sum(pg_column_size(row_alias))` over base-table rows. This measures PostgreSQL's composite row datum, including the representation returned for TOASTed attributes, but it does not separately add indexes, relation/page overhead, free space or TOAST relation overhead. A future formula requires a new measurement version and a clean generation.
- Each source has a statement timeout. A retry overwrites the same staging key.
- Dropped partitions disappear during source discovery, so a new generation naturally stops charging their rows. A leaf dropped after discovery contributes an empty measurement: only SQLSTATE 42P01 on a confirmed-missing leaf is treated this way (other "does not exist" errors, such as a missing column, fail the unit), and a manifest leaf that is no longer in the catalog is accepted only if its name matches the exact partition naming pattern of its own parent table.
- A unit that exceeds the job budget or its statement timeout is counted as a failure, logged with its key and surfaced in health (`storage_usage_failures_total`, `storage_usage_unit_timeouts_total`, and `storage_usage_stuck_attempts`, the consecutive failed attempts of the same unit). A budget that merely ends after earlier units made progress is not a failure.
- Pending work is counted with `COLLATE "C"` so it matches the reconciler's byte-order cursor under any database collation.

No `pg_total_relation_size` value is presented as tenant usage. No admin request scans analytics tables.

### Replay sources

The blob store is the reconciliation authority because `replay_sessions.bytes` can lag when asynchronous index updates are dropped and it excludes CSS. Both disk and S3 implementations list site-addressable recording and CSS prefixes and return object sizes. The reconciler stages recording and CSS totals separately.

Existing upload/deletion counters may later provide faster deltas, but the first release remains correct after crashes by reconciling from blob storage. Listing is background-only, bounded/paginated where the backend supports it, and covered by the disk/S3 contract tests.

### Atomic publication

Each generation measures the site set captured at its start. Its source scans are intentionally best-effort at their individual statement times, not one database snapshot held across the whole run: ingestion and retention continue normally, and the next generation corrects concurrent movement. After every registered source completes, one short transaction:

1. aggregates staging rows by site;
2. upserts snapshots for the captured, still-unerased site set with a shared publication `measured_at` and measurement version;
3. writes zero snapshots for captured sites with no staged data;
4. removes snapshots/staging data for every site that is erased or has its erasure claimed, while leaving newly created sites unknown. Publication share-locks the captured site rows and skips claimed sites, so it cannot re-insert a snapshot concurrently with erasure; any orphan left by an earlier interleaving is removed here;
5. marks the run complete.

The job uses the existing scheduler advisory-lock model. It runs frequently enough to resume bounded work, starts a new generation only when the last complete generation is 24 hours old, and never performs external blob I/O inside a database transaction.

## Admin API

Extend existing admin organization and site responses instead of adding live-calculation endpoints. Both receive a nullable `storage` object:

```json
{
  "estimated_storage_bytes": 1234,
  "database_bytes": 1000,
  "replay_bytes": 234,
  "measured_at": "2026-10-26T12:00:00Z",
  "measurement_version": 1
}
```

The `AdminSite` storage object carries the internal category breakdown, including in the existing organization-detail site list, so an administrator can diagnose growth without a new site-detail endpoint. Organization list/detail values are aggregated directly from all non-erased site snapshots, including restorable sites. If any included site has no snapshot, the organization storage object is null rather than a misleading partial total.

The OpenAPI document is the source of truth; generated Go and TypeScript clients are regenerated, not hand-edited.

## Admin UI

- Add a **Storage** column to Admin Sites and show database/replay breakdown in the existing detail surface.
- Add total **Storage** to Admin Organizations and per-site storage in organization detail.
- States are `Calculating…`, formatted total with a database/replay tooltip, and `Updated …` freshness text.
- Add the strings to English, Spanish, German and Bengali locale files.
- No new route or dashboard card is introduced in the first release.

## Observability and failure handling

Expose only process-level metrics: last successful run, current run duration/status, pending sources, failure count, unit timeouts and the stuck-attempts gauge. Never use `site_id` as a metric label. Health/readiness endpoints do not calculate usage.

Errors retain the last good snapshot. A run can resume from its persisted source cursor. A changed source registry or estimator version starts a clean generation. Operators can identify stale values from `measured_at` and logs.

## Acceptance criteria

- An admin sees estimated storage for sites and organizations after a reconciliation, including recording and CSS blob bytes.
- Unknown storage is never rendered as zero; a failed partial run never publishes partial totals.
- Re-running any source or resuming a run produces the same result.
- Soft-deleted data counts until erasure; erased data does not.
- Reading the new storage fields uses only indexed site/snapshot tables and never initiates an analytics or blob-storage scan. The pre-existing Admin Sites lifetime-pageview aggregate is unchanged.
- The source-registry contract catches newly added tenant-owned tables.
- The reconciler handles partition removal, TOAST-heavy fixtures, duplicate CSS, replay index drift, and values beyond 32-bit range.
- Targeted tests, the full Go suite, `go vet`, web tests/typecheck/build and migration/OpenAPI drift gates pass.
