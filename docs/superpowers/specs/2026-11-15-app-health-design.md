# App Health: crash, error and compatibility tracking for apps

Date: 2026-11-15. Status: accepted (2026-11-15).

## Intent

Supermetry today measures websites. Organisations also ship desktop, mobile
and backend apps, and need to know when those apps crash, fail to install, hit
an incompatible device, or throw errors, and which release, OS, arch and
device cause it. App Health adds that: a privacy-first, Sentry-lite issue
tracker that lives inside the same organisation, next to the analytics.

The first real client is VoiceStudio (debpalash/VoiceStudio PR #4137). It
already sends opt-in `diagnostic_report` events to Supermetry's public intake
from Electron, its Python backend, iOS and Android. The feature must serve
that client unchanged **and** be app/project agnostic, so any organisation
can report from any app.

## Goals

1. Any app (desktop, mobile, CLI, backend) can report crashes and errors to a
   Supermetry "app" through a documented, stable HTTP format.
2. Reports are grouped into **issues** (by fingerprint) with status: open,
   resolved, ignored, regressed.
3. Release health: crash-free users and crash-free sessions per release.
4. Breakdowns by release, platform, OS, arch, device class and any allowed tag
   (the "incompatibility" view: which combinations fail).
5. Alerts: new issue, regression of a resolved issue, issue/crash-rate spike,
   delivered through the existing alert channels.
6. VoiceStudio's current payload works with no client change.
7. Reference clients (TypeScript/Electron, Python, Swift, Kotlin) that bake in
   the PR's design: consent gate, bounded durable queue, retry, dedup.

## Non-goals (for this project)

- Native crash dumps (minidumps), symbol/source-map upload, symbolication.
- Performance tracing, spans, distributed traces.
- Session replay, breadcrumbs, logs, screenshots, attachments.
- Free-text error messages as a stored field (see Privacy).
- Issue assignment workflows, comments, integrations with issue trackers.

## Anti-goals (a failure even if it works)

- Storing personal data: raw IPs, file paths with user names, free-text
  messages, URLs with query strings, audio/text content.
- Any new heavy dependency: no ClickHouse, Kafka, separate workers or Node in
  production (CLAUDE.md hard rule 1).
- Silent loss: a report the server drops must be counted and visible to the
  site owner, not swallowed by a bare 202.
- VoiceStudio-specific code paths in the core. VoiceStudio is mapped through
  the generic format, not special-cased in queries or UI.

## Constraints

- Go binary + Postgres only. Raw tables partitioned monthly, BRIN on time,
  retention by DROP PARTITION, rollups idempotent, buckets in the site TZ.
- Intake hot path does no DB reads (site lookups come from the in-memory
  registry, as today).
- Licensing: ideas only from Plausible/OpenPanel/GoatCounter/Vince; Sentry's
  public protocol docs may inform field naming, but no Sentry code is copied.
- Tests first for intake mapping, fingerprinting, crash-free formulas and
  rollups, each against hand-computed fixtures.

## Findings that shaped this (verified 2026-11-15)

A compatibility test (`internal/ingest/diagnostics_compat_test.go`, real
handler + Postgres) sent the PR's exact payload:

- Accepted and stored intact: 22 props, 1737-byte `frames`, `occurrences`,
  dedup by `id`, `identify` traits, queryable by `prop:error_code`.
- **Dropped silently (202):** `ts` older than 24 h
  (`internal/ingest/handler.go:180-185`). The client queues up to 7 days.
- **Dropped as bot (202):** Electron-default, curl, Go and empty user agents
  (`internal/enrich/bot.go:64-77`). A custom `VoiceStudio/x.y.z (...)` UA passes.
- **Rejected 400:** `url` host not in the site's `allowed_hosts`.
- **Props stripped (202):** anonymous tracking mode or site profiles off
  (`handler.go:122-134, 192-200`).

## Approaches considered

| | Approach | Trade-off |
|---|---|---|
| **A (picked)** | Standard crash fields + bounded free-form tags, plus a per-app tag allowlist | Uniform dashboards for every app; tag views are generic, not product-tailored |
| B | Each app uploads its own closed schema (like VoiceStudio's `diagnostics.json`) | Strongest lockdown, but heavy to build/version and a hurdle at onboarding |
| C | Custom events + dashboard presets, no new storage | Cheapest; no issue status, slow crash-free math, Sentry-lite in name only |

The user picked A on 2026-11-15. The tag allowlist borrows B's privacy benefit
without making a schema file mandatory.

## Design

### 1. App as a site kind

- Add `KindApp` beside `KindWeb` and `KindPixel` (`internal/sites/registry.go:21`),
  fixed at creation. An app has a name, slug, TZ, retention, and no domain.
- Apps get a **public ingest key** (write-only, safe to embed in binaries,
  rotatable). The payload's `site` field carries the app's public id; the key
  travels in a header. `allowed_hosts` and strict-origin checks do not apply to
  app kind; the key replaces them.
- Per-app settings: tag allowlist (empty = accept any tag up to the caps),
  max report age (default 7 days, max 30), environments list (e.g.
  `production`, `beta`).
- An existing web site can keep receiving `diagnostic_report` events (the
  VoiceStudio path today); see §3.

### 2. Wire format (universal, versioned)

`POST /api/public/v1/app-events` (new), JSON, max 16 KB, batch of up to 20
events per request. Each event:

| Field | Type | Notes |
|---|---|---|
| `id` | string ≤64 | Client event id; dedup key |
| `ts` | int ms | Accepted back to the app's max report age |
| `kind` | enum | `crash`, `error`, `hang`, `install_failed`, `install_stalled`, `incompatibility`, `operation_failed`, `session` |
| `level` | enum | `fatal`, `error`, `warning` |
| `handled` | bool | |
| `release` | string ≤64 | App version |
| `build` | string ≤64 | Commit or build number |
| `environment` | string ≤32 | |
| `platform` | enum | `windows`, `macos`, `linux`, `ios`, `android`, `web`, `other` |
| `os_version` | string ≤32 | Major.minor only |
| `arch` | enum | `x64`, `arm64`, `x86`, `arm`, `other` |
| `device_class` | string ≤32 | e.g. `cuda`, `cpu`, `phone`, `tablet` |
| `runtime` | string ≤32 | e.g. `electron`, `python`, `swift`, `kotlin` |
| `error_type` | string ≤64 | Exception class name |
| `exit_reason` / `signal` / `exit_code` | enums / int | For crashes |
| `frames` | string ≤4 KB | Newline-separated `path:line function`, first-party, no absolute paths |
| `fingerprint` | hex ≤64 | Optional; server computes if absent |
| `occurrences` | int ≥1 | Client-side coalescing count |
| `install_id` | string ≤128 | Pseudonymous install/profile id, for crash-free users |
| `session_id` | string ≤64 | Pseudonymous app session, for crash-free sessions |
| `tags` | map | ≤20 keys, key ≤64, value ≤200 bytes, string/number/bool |

`kind: session` is a lightweight "session started/ended ok" ping that powers
crash-free sessions; apps send one per run.

Responses: `202` with `{"accepted":n,"dropped":[{"index":i,"reason":"..."}]}`,
so clients learn about drops. Reasons are a closed list (`too_old`,
`invalid`, `tag_not_allowed`, `rate_limited`, `duplicate`). Drops also feed the
existing drop stats so owners see them in the UI.

### 3. VoiceStudio compatibility mapping

The existing `/api/public/v1/events` keeps working. When an event named
`diagnostic_report` arrives for **any** site kind, the intake maps it to the
App Health format rather than (or in addition to; builder's call, see below)
the generic custom event:

- `category` → `kind` (`app_crash`/`backend_crash`/`renderer_crash` → `crash`;
  `install_failed`, `install_stalled`, `incompatibility` keep their names;
  `generation_failed`, `model_load_failed`, `engine_failed`, `download_failed`,
  `operation_failed` → `operation_failed` with `tags.category` = original).
- `app_version` → `release`, `build_commit` → `build`, `platform`, `arch`,
  `device_family` → `device_class`, `error_type`, `reason` → `exit_reason`,
  `signal`, `exit_code`, `frames`, `fingerprint`, `occurrences`, `profile_id` → `install_id`.
- Remaining props (`source`, `stage`, `error_code`, `engine_id`, buckets,
  `schema_version`) → `tags`.
- For this event name only, the late-drop window uses the site's max report
  age (default 7 days) instead of 24 h.

The mapping is a table in one file plus tests; no VoiceStudio names appear
elsewhere in the core.

### 4. Intake behaviour fixes (also benefit web sites)

- Late drops call `recordDrop` so they show in the drop stats UI.
- Document required client UA (`AppName/version (platform; arch)`); app-kind
  intake skips the browser bot filter (the key is the trust signal), web-kind
  intake keeps it.

### 5. Storage

- `app_events` (raw, monthly partitions, BRIN on `ts`, retention by DROP
  PARTITION): site_id, ts, kind, level, handled, issue_id, release, build,
  environment, platform, os_version, arch, device_class, runtime, error_type,
  exit fields, install_hash, session_hash, occurrences, tags jsonb, frames
  (stored once per issue sample, see below).
- `app_issues`: site_id, fingerprint (unique per site), kind, error_type,
  title (derived: `error_type` + top frame, never a free-text message),
  status, first_seen, last_seen, first_release, last_release, resolved_in_release,
  event_count, sample_frames, updated_at.
- `app_issue_rollup_hourly` / daily per (issue, release, platform, arch,
  device_class): events, distinct installs (HLL or exact set per bucket,
  builder's call against the scorecard), recomputed idempotently for closed buckets.
- `app_session_rollup_daily` per (site, release, platform): sessions,
  crashed sessions, installs, crashed installs, for crash-free rates.
- install_id / session_id are hashed (HMAC-SHA256) with a **per-app secret**
  before storage, giving stable pseudonyms so installs dedupe across days and
  late reports. The secret is destroyed when the app's data passes retention
  (and on app deletion), which unlinks old hashes. This is deliberately
  weaker than web visitors' daily salts; see "Decided by the user".
- Issues are upserted in the writer's batch flush (not on the request path).

### 6. Fingerprinting

If the client sends `fingerprint`, use it. Otherwise
`sha256(kind | error_type | normalised top 5 frames)`, where normalisation
strips line numbers and anything that looks like an absolute path. A
regression is an event for a `resolved` issue whose release is newer than
`resolved_in_release` (or any event, if that is empty).

### 7. Dashboard (per app, under the org)

- **Overview:** crash-free users / sessions (current release vs previous),
  events over time by kind, top new and top regressed issues.
- **Issues list:** filter by status, kind, release, platform, environment,
  tag; sort by last seen, events, users affected.
- **Issue detail:** title, status actions (resolve / resolve in next release /
  ignore / reopen), frames, events and users over time, breakdown tables by
  release, platform, OS, arch, device class and each tag.
- **Compatibility:** matrix of platform × arch × device class (rows) vs
  kind/error type (columns), with failure rate where session data exists.
- **Releases:** per release adoption, crash-free rates, new issues introduced.

Built with the existing stack (React + TanStack + shadcn), API in
`internal/api/openapi.yaml`.

### 8. Alerts

New alert kinds in `internal/alerts`: `app_new_issue`, `app_issue_regressed`,
`app_crash_free_below` (threshold %, per release), `app_issue_spike`. They use
the existing channels and delivery history.

### 9. Client kits

Small reference clients in a new `sdk/` tree (or a separate repo, builder's
call), each: explicit consent gate (off by default), bounded durable queue,
batched send, 3 attempts with backoff, TTL = max report age, dedup id, custom
UA, frame sanitiser that drops absolute paths. Each SDK is MIT-licensed so
apps can vendor it.

### Privacy

- No free-text message field. `title` is derived server-side.
- Frames are sanitised server-side too (reject absolute paths, strip home dirs).
- Tags are capped and optionally allowlisted; dropped tags are counted.
- IPs are used for country only (existing GeoIP), never stored.
- install/session ids are HMAC pseudonyms with a per-app secret (§5).

## Delivery plan (phases, each its own PR)

1. Intake fixes: late-drop counting, `diagnostic_report` 7-day window, docs on UA.
2. App site kind + ingest key + settings (migration, sites, API, minimal UI).
3. `app-events` endpoint, VoiceStudio mapping, `app_events` + `app_issues`
   tables, fingerprinting, writer upserts.
4. Rollups and crash-free formulas (test-first with fixtures).
5. Dashboard: overview, issues list, issue detail.
6. Compatibility matrix + releases page.
7. Alerts.
8. Client kits + docs + `docs/features.md` update.

**Visible progress (user, 2026-11-15):** the "new site" page
(`/app/{org}/sites/new`) is updated as phases land. Phase 2 adds the "App"
kind choice there (with platform hint, ingest key shown once, snippet per
SDK); later phases extend its setup steps (tag allowlist, report age,
"send a test report" check). After each merged UI phase, rebuild
`./bin/supermetry` and restart the running instance (ana.voicestudio.sh) so
the user can check it.

## Decided by the user

- App/project agnostic, while covering everything VoiceStudio needs (2026-11-15).
- Approach A: standard fields + tags + optional allowlist (2026-11-15).
- Install identity: stable per-app pseudonym (HMAC with a per-app secret,
  destroyed with retention), not daily salts (2026-11-15).
- Cloud quota: app events count toward the organisation's existing event
  quota, same as web events; no separate App Health plan (2026-11-15).
- Visible progress: update `/app/{org}/sites/new` as phases land and rebuild
  the running instance after each UI phase (2026-11-15).

## Calls made by Claude after the builder check (2026-11-15, user may override)

- **VoiceStudio routing:** a web site can be *linked* to an app. A
  `diagnostic_report` arriving on `/events` for a linked web site passes the
  web site's normal checks (public id, allowed hosts, strict origin), then is
  mapped and stored under the **linked app's** site_id. No client change; the
  owner adds the app host to allowed hosts and links the app once. Unlinked
  web sites keep storing it as a plain custom event.
- **Ingest key:** header `X-Supermetry-Key: smk_<base32 24 bytes>`; only the
  sha256 is stored; rotation keeps the old key valid for 7 days. Not a secret:
  abuse is bounded by rate limits per key (default 600 events/min) and per
  client IP (60/min, IP held in memory only), with the `rate_limited` reason.
  The legacy `/events` path stays key-less (same trust level as web today).
- **Late events and rollups:** reports older than min(max report age, app
  retention) drop as `too_old`. The rollup job recomputes every bucket within
  the max report age (lookback), so late events reopen closed buckets
  idempotently. Issue upserts use `greatest(last_seen, ts)` and
  `least(first_seen, ts)`.
- **Release ordering:** semver if every compared release parses; otherwise
  first-seen time of the release. Used for regressions, "resolve in next
  release" and current-vs-previous.
- **Sessions:** SDKs send `kind: session` at start. A session is *crashed* if
  any `kind: crash` or `level: fatal` event carries its session_id; handled
  errors don't count. Crashes with no matching ping still count as crashed
  sessions. Apps that send no pings (VoiceStudio today) show crash-free
  **users** only; crash-free sessions shows "n/a".
- **Alerts:** all four kinds are evaluated by the existing evaluator on its
  cadence (polled, no writer → alerts coupling). Spike = last hour vs the
  same hour's 7-day average, ×3 and ≥10 events by default. Crash-free alerts
  are per app, on the latest release with ≥100 installs.
- **Batch responses:** malformed JSON, unknown app or bad key → 4xx for the
  whole request; otherwise 202 with per-event drops. Dedup uses the existing
  in-memory dedup (window, not forever). New drop reasons extend dropstats.
- **Tag cardinality:** max 1,000 distinct values per tag key per app per day;
  values beyond it are stored as `(other)` and counted as a drop reason.
  The allowlist applies at ingest only.
- **Frames:** scrubbed, not rejected. Absolute paths are reduced to the path
  after the last known project marker, or to the basename; home dirs and
  drive letters are removed. Fingerprints use the scrubbed frames.
- **SDKs:** TypeScript/Electron and Python ship fully tested in phase 8;
  Swift and Kotlin ship if toolchains are on the machine, else as a
  follow-up PR. All MIT.

## Left to the builder

- Whether a mapped `diagnostic_report` is also kept as a custom event.
- Distinct-install counting method (HLL vs exact), per scorecard numbers.
- Exact table indexes, proven with EXPLAIN on realistic data.
- SDK location (in-repo `sdk/` vs separate repo).
