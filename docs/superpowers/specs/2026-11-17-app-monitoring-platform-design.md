# App Monitoring platform: program design and roadmap

Date: 2026-11-17. Status: **proposed** (needs the decisions in §10 before Phase 1 plans are written).
Supersedes the scope of `2026-11-15-app-health-design.md` (App Health v1 stays the base and keeps working).
Folds in `docs/superpowers/plans/2026-11-17-mcp-app-health.md` as Phase 1's first PR.

## 1. Objective

Supermetry becomes one product with two experiences on one data model:

- **Product Analytics:** what users do (web today; apps added).
- **App Monitoring:** when apps break, why, and who is affected (Sentry as the feature benchmark, GlitchTip as the self-hosting benchmark).
- **Observability**, later, ties both together with traces, logs and metrics (SigNoz as the benchmark), without matching all of SigNoz.

**Test of success:** a team on Sentry or GlitchTip, plus basic product analytics, switches to Supermetry by **changing the DSN**, keeping its existing Sentry SDK instrumentation, and loses nothing it uses day to day.

## 2. Hard constraints that shape everything

These come from `.claude/CLAUDE.md`. Changing one needs a recorded decision.

- **Go binary + Postgres only.** No ClickHouse, Kafka, separate workers or Node in production. Symbolication, rollups and alert evaluation run as in-process goroutines with bounded queues and counted drops.
  - SigNoz and Sentry depend on ClickHouse or Kafka (inference; verify in Phase 0). We compete on simplicity and cost, not on petabyte scale.
  - Each phase has a stated throughput target, measured on the demo VM spec, before merge.
- **Postgres layout.** Monthly partitions, BRIN on time, `DROP PARTITION` retention, idempotent rollups bucketed in the project's time zone, no DB reads on the ingest hot path.
- **Privacy.** We never persist a raw IP or a full user agent. Salts and secrets are stored and rotated. Server-side scrubbing is always on.
- **Licensing.**
  - Sentry's server is not open source in our sense (FSL/BSL; verify), and SigNoz has proprietary `ee` code (verify): **ideas and protocol behaviour only**.
  - Sentry SDKs are MIT. We may run them in tests and dev tooling, never vendor them into the binary.
  - GlitchTip is MIT (verify), but it is Python, so we re-implement in Go.
  - Phase 0 clones GlitchTip into `knowledge/` (read-only) and records licences in `.workspace/findings.md`.
- **Evidence.** Every competitor claim in user-facing docs needs a source ref. Anything unverified here is marked *inference*.

## 3. Audit: what exists (core `main` @ 375275a9)

| Area | Exists | Reuse | Gap for this program |
|---|---|---|---|
| Projects | `sites` with `kind` web, pixel or app. Org/team membership, per-site API keys with scopes (`stats:read`, `data:export`, …), site restriction | Keep `sites` as the project table; app = project | Several platforms per project already work (the `platform` column). Need project-level roles for issue actions (Phase 1b). |
| Error intake | `POST /api/public/v1/app-events` (own JSON format). Keys `smk_…` (sha256, 7-day rotation). In-memory checks, CopyFrom writer, drop reasons (`too_old`, `invalid`, `duplicate`, `rate_limited`), HMAC install/session hashes, tag allowlist and cardinality cap | Writer, key store, drop stats, HMAC hashing, scrubber | No Sentry envelope/store endpoints. No messages, exception chains, breadcrumbs, contexts, request data, `user`, SDK info, trace ids. |
| Storage | `app_events` (narrow row, monthly partitions, kinds crash/error/hang/install_*/incompatibility/operation_failed/session). `app_issues`, `app_issue_daily/hourly`, `app_release_daily`. Retention by partition | Keep `app_events` as the narrow, hot event index | Need a heavy payload table, issue workflow tables, releases, deploys, sessions, debug files, spans, logs. |
| Grouping | sha256(kind, error_type, top 5 normalised frames) | Keep as the fallback strategy | In-app frame detection, SDK `fingerprint` overrides, server grouping rules, merge/unmerge, grouping versioning. |
| Issue workflow | open, resolved, resolved-in-next-release, ignored, regressed, with `regressed_at` | Status machine | Assignment, comments, activity log, snooze conditions, priority, saved searches, search syntax, bulk actions, ownership rules, tracker links. |
| Release health | Crash-free users and sessions from raw `app_events`; "session" is a ping kind; semver or first-seen ordering | Ordering (`internal/apprelease`), queries (`internal/appquery`) | Real session lifecycle (Sentry session items: ok, exited, crashed, abnormal, errored), release/deploy API, adoption, rollout, "introduced in". |
| Symbolication | none (frames are text, scrubbed) | — | Source maps, ProGuard/R8, dSYM, ELF/DWARF, later PDB and minidumps. Debug-file storage. |
| Product analytics | Full web stack in `internal/query`: funnels, retention, journeys, cohorts, segments, goals, revenue, dashboards, CSV, MCP. Built on `events`/`sessions` (web semantics, daily salts, 24 h window) | Query algorithms and UI components | Apps need stable install ids (no daily salt), offline queues (days late), screens instead of pages, version and platform adoption. |
| Performance | Web Vitals for web sites only | Vitals UI patterns | Transactions, spans, percentiles, slow transactions, OTLP. |
| Alerts | Engine, channels (email, webhook, Slack, Discord, Telegram), templates, history. App kinds: new issue, regression, crash-free below, issue spike | Engine and channels | Rule builder (conditions, filters, actions), routing, silences, maintenance windows, performance and metric alerts. |
| MCP | `POST /api/public/v1/mcp`, 7 web tools, `stats:read`, tenant checks | Handler, auth, schemas | App, issue, release, performance and product tools (§8). |
| Clients | `sdk/typescript`, `sdk/python` (own format); VoiceStudio integration | Keep for our own format | Strategy shifts to **Sentry SDKs first** (§5); our kits stay small and are deprioritised. |
| Cloud / site | Cloud gateway proxies the intake, counts `app_events` in the quota; site docs synced | Gateway pattern | New intake paths in the gateway, separate quotas per data class, docs and migration guides. |

## 4. Architecture

### 4.1 One pipeline, many front doors

```
Sentry envelope / store ─┐
OTLP/HTTP (traces, logs) ┼─► normalise ─► scrub ─► (symbolicate) ─► group ─► bounded queues ─► CopyFrom writers
Own app-events JSON ─────┘    (in-memory only: key → project, rate limit, dedup cache)          │
                                                                                                ├─► app_events (+ bodies)
                                                                                                ├─► app_sessions
                                                                                                ├─► spans / logs
                                                                                                └─► app_product_events
rollups (10-min lookback, idempotent) ─► alert engine ─► channels
```

- **Front doors.** Each one only parses its format and emits a common internal `Envelope` (events, sessions, spans, logs, client reports). Normalisation, scrubbing, grouping and writing are shared.
- **Keys.** A Sentry DSN is `https://<public_key>@<host>/<project_id>`. The public key is a new key type on `app_ingest_keys` (it reuses the store, rotation and registry cache).
  - Our `smk_` keys keep working on `/app-events`.
  - Whether `project_id` must be numeric for every SDK is checked in Phase 0 (inference: some SDKs parse it as an integer).
- **Back-pressure.** Reply 429 with `Retry-After` and `X-Sentry-Rate-Limits` per data category, so SDKs back off as they do against Sentry.
  - Accept the `client_report` items SDKs send and show the client-side drops next to our server-side drops in Health.
- **Symbolication runs before grouping.** Events with unresolved native or minified frames go to a bounded in-process symbolication queue. If symbols are missing, the event is grouped on raw frames and flagged `symbolication: missing`. Uploading the files later regroups new events only; the history is not rewritten.

### 4.2 Data model (new and changed tables)

All event-like tables are partitioned monthly by `ts`, with BRIN indexes and a btree only where a query needs one.

| Table | Purpose | Default retention |
|---|---|---|
| `app_events` (extended) | Narrow index row per error/crash. New columns: `message_hash`, `level`, `culprit`, `transaction`, `trace_id`, `span_id`, `user_hash`, `sdk_name`, `sdk_version`, `dist`, `symbolication` | 90 d (configurable) |
| `app_event_bodies` (new) | Scrubbed payload, jsonb: exception chain with frames and source context, breadcrumbs (≤100), contexts (os, device, app, runtime, gpu, culture), request (no query string, no cookies, no auth headers), tags, scrubbed message | 30 d (configurable) |
| `app_issues` (extended) | `priority`, `assignee_user_id`/`assignee_team_id`, `substatus` (snoozed, archived_until_escalating), `snooze_until`/`snooze_count`, `merged_into`, `first_release`, `last_release`, `level`, `culprit`, `grouping_version` | life of project |
| `app_issue_activity` | Comments, status changes, assignments, merges, linked tracker issues, regression notes | life of issue |
| `app_issue_hashes` | Fingerprint → issue mapping (makes merge/unmerge and grouping upgrades possible) | life of issue |
| `app_grouping_rules`, `app_ownership_rules`, `app_saved_searches` | Per-project config | — |
| `app_releases`, `app_deploys`, `app_release_commits` | Release API, deploy markers, suspect commits | life of project |
| `app_sessions` | One row per session (sid hash, did hash, release, env, platform, status, started, duration, errors). Sentry `sessions` aggregates expand into counted rows | 90 d |
| `app_session_daily` | Rollup: sessions and users by status × release × env × platform. The source of crash-free rates and adoption | 25 mo |
| `app_debug_files` | Metadata (debug id, kind: sourcemap, proguard, macho, elf, pe, breakpad; size; release). Blobs go in the existing object/blob store the backup and replay code uses (verify which) | until deleted |
| `app_product_events` | Custom events and screen views for apps (§4.4) | per plan |
| `spans` | trace id, span id, parent, transaction/segment name, op, description (scrubbed, parameterised), start, duration, status, release, env, a few attributes | 14 d |
| `span_hourly` | Rollup per transaction × op × release: count, errors, a log-scale histogram (fixed buckets, mergeable) → p50/p75/p90/p95/p99 | 25 mo |
| `logs` (Phase 6) | OTLP logs, scrubbed body, severity, trace id, attributes (capped). Search with `pg_trgm` (a contrib extension, allowed) | 7 d |

Retention and sampling policy: the retention per data class is configurable per project, within plan limits in Cloud. We honour SDK-side sampling (`tracesSampleRate`); server-side dynamic sampling comes later.

### 4.3 Privacy pipeline (applies to every front door)

Defaults are strict, and a project owner can relax them per project. This reverses the v1 anti-goal "no free-text messages", so it is **decision D1**.

- **IP:** used for country/region and rate limiting, never stored. `user.ip_address` is treated the same way.
- **User:** `user.id` is stored as HMAC (the per-project secret, as for installs). `email`, `username`, `name` and `geo` are dropped by default.
- **Messages and exception values:** pattern scrubbing for emails, tokens/JWTs/keys, card numbers, IPs, home-directory paths, long digits and UUIDs (configurable). We store the scrubbed text plus a hash. The per-project "Store messages" setting is on by default with scrubbing (D1).
- **Request data:**
  - drop cookies, `Authorization` and `Proxy-Authorization`, and any header on a denylist;
  - drop query strings and bodies;
  - normalise URLs to their routes.
- **Stack locals (`vars`):** dropped by default.
- **Breadcrumbs:** keep category, type and level; scrub the message. Cap at 100 entries and 16 KB.
- **Proof:** the end-to-end test from v1 (a `pg_dump` contains no raw identifiers or secrets) is extended to every new table and front door.

### 4.4 Product analytics for apps (decision D3)

Two paths:

- **A (recommended, gated by a Phase 0 spike).** App product events and sessions go into the *existing* `events`/`sessions` tables under the app project. A new ingest adapter maps:
  - visitor id → install HMAC;
  - path → screen name;
  - hostname → platform;
  - custom props → release, OS and device.

  The adapter skips the web-only checks: hostname allowlist, bot user agent, daily salt and the 24 h window. Funnels, retention, journeys, cohorts, segments, dashboards, CSV and MCP then work for apps with little new code. Web-only cards (referrers, UTM, bounce) are hidden for app projects.

  The spike must prove that late (multi-day) inserts close and roll up correctly, and that profile semantics hold.
- **B (fallback).** A separate `app_product_events` table plus app-specific query code. This is cleaner semantically but duplicates the funnel, retention and journey engines.

App-specific metrics on either path:
- DAU/WAU/MAU and stickiness;
- version and platform adoption;
- feature adoption (a saved event set);
- session duration (from `app_sessions`).

### 4.5 Dashboard information architecture

```
Org
├── Web Analytics      (web projects: today's dashboard)
├── App Monitoring     (app projects)
│   ├── Overview  ├── Issues  ├── Releases  ├── Crashes (crash/ANR/hang filter of Issues)
│   ├── Performance  ├── Sessions & Users  ├── Compatibility  ├── Events (product)  └── Alerts
├── Observability      (later: Logs, Traces, Metrics, Dashboards)
└── Settings           (SDK setup with DSN, Environments, Keys, Symbols, Integrations, Data & privacy)
```

The project switcher shows both kinds. A combined "web + app" view for a product with a website and an app comes later, via a project link (`app_link` already exists).

## 5. Compatibility targets

| Standard | Scope | Phase |
|---|---|---|
| Sentry ingest | `POST /api/{project_id}/envelope/` and `/store/`; auth through the `X-Sentry-Auth` header or the `sentry_key` query parameter; gzip/deflate/zstd bodies (brotli only if an SDK needs it; verify); item types `event`, `transaction`, `session`, `sessions`, `client_report`, `attachment` (accepted and size-capped; minidumps in 2c); others accepted and dropped with a count | 1a (events), 2a (sessions), 4 (transactions) |
| SDK matrix (CI fixtures) | JavaScript (browser, Node, Electron), Python, Go, Java/Android, Cocoa (iOS/macOS), React Native, Flutter/Dart, .NET, native (C/C++). Each: recorded envelopes become golden tests; live runs for JS/Python/Go via `make sdk-compat` | 1a for the first five, then each phase |
| Release and deploy API | The endpoints `sentry-cli` uses for `releases new/finalize/deploys` and `sourcemaps upload` / `debug-files upload` (artifact bundles, chunk upload). Exact endpoints recorded in Phase 0 (inference that the chunk-upload API is needed) | 2b, 2c |
| Source maps | JS/TS, by debug id or by release + dist + URL. Own VLQ decoder (small; avoids a dependency) | 1c |
| ProGuard/R8 | `mapping.txt` by UUID | 2c |
| Apple dSYM, ELF | Mach-O/ELF + DWARF line tables via Go stdlib (`debug/macho`, `debug/elf`, `debug/dwarf`) | 2c |
| Windows PDB, Breakpad/Crashpad minidumps | Needs a PDB reader and CFI stack walking (large). Breakpad `.sym` text files are a cheaper path | 2d (decision D5) |
| OpenTelemetry | OTLP/HTTP (protobuf and JSON) `/v1/traces` first, then `/v1/logs` and `/v1/metrics`. Exceptions in spans become issues. New deps: `google.golang.org/protobuf`, `go.opentelemetry.io/proto/otlp` (Apache-2.0) | 4, 6 |
| Webhooks and trackers | Outbound issue webhooks (exists for alerts); GitHub issue link/create via the existing integrations | 1b, 5 |
| Own format | `/api/public/v1/app-events` and `diagnostic_report` mapping stay supported (VoiceStudio) | — |

## 6. Phases

Each phase gets its own `writing-plans` plan before code. Each lands as stacked PRs with tests, docs, a changelog entry, the `/sites/new` setup flow updated and MCP tools added. Each must pass a **bench gate** on the demo VM spec.

| Phase | Scope | Exit criteria |
|---|---|---|
| **0. Foundations** (no product code) | (a) Evidence: GlitchTip clone in `knowledge/`; Sentry protocol, sentry-cli and SigNoz feature notes with refs in `findings.md`; licences. (b) Capture server + recorded envelopes from the SDK matrix. (c) Spike D3-A (late app events through the web pipeline). (d) Baseline ingest benchmark on the current `app-events`. (e) Final data-model migration list. (f) Navigation skeleton (Web Analytics / App Monitoring) behind a flag | Findings committed; fixtures in `internal/appingest/testdata/sentry/`; D1–D7 answered; spike verdict; bench numbers recorded |
| **1a. Sentry-compatible error intake** | DSN keys; envelope/store endpoints; normaliser; privacy pipeline (§4.3); `app_event_bodies`; grouping v2 (in-app frames, SDK `fingerprint`, `{{ default }}` merging, versioned); dedup; 429 with rate-limit headers; client reports; event detail UI (exception chain, frames with source context, breadcrumbs, contexts, tags); the 5 MCP App Health tools (existing plan) | The JS, Python, Go, Android and Cocoa SDK fixtures pass; a live JS/Python/Go SDK pointed at a DSN creates grouped issues; pg_dump privacy test passes; ≥ 1,000 events/s sustained (target, confirmed against the Phase 0 baseline) |
| **1b. Issue management** | Assignment (user/team), comments, activity timeline, priority, snooze (until time, N events, N users), archive until escalating, merge/unmerge, grouping rules UI, ownership rules (path/module/tag → owner), search syntax (`is:unresolved release:x platform:ios assigned:me level:fatal`), saved views, bulk actions, GitHub issue link, issue webhooks | Role checks tested (viewers read-only); search parser fuzzed; merge/unmerge keeps counts exact |
| **1c. JS source maps** | Upload API + sentry-cli-compatible upload (verified in 0); debug-id and release/dist/URL lookup; source context in frames | Minified fixture from a real bundle symbolicates to the original file and line |
| **2a. Release health v2** | Session ingestion (`session`, `sessions`); `app_sessions`, `app_session_daily`; crash-free users/sessions from sessions (v1 ping kept as a fallback); adoption; unhandled vs handled; ANR (Android) and app-hang (iOS) as kinds from the SDK's mechanism; app start metrics | Fixtures from the Android/Cocoa/JS SDKs give hand-computed crash-free rates; the v1 VoiceStudio path still works |
| **2b. Releases and deploys** | Release/deploy/commit API; deploy markers on charts; rollout monitoring (adoption × crash-free over time); "introduced in" and suspect commits (commits in the first-seen release touching in-app frame paths); release comparison | sentry-cli `releases`/`deploys` against Supermetry works |
| **2c. Native and mobile symbolication** | Debug-file store; ProGuard/R8; dSYM and ELF (DWARF); symbolication queue and missing-symbols banner; per-platform breakdowns | Android obfuscated, iOS, macOS and Linux fixtures symbolicate |
| **2d. Windows/native crashes** (D5) | Minidump attachments, Breakpad `.sym` first, PDB later | Decided in D5 |
| **3. Product analytics for apps** | D3 path; DAU/WAU/MAU, stickiness, custom events, retention, funnels, journeys, feature/version/platform adoption, geo/device, engagement; Sentry SDK sessions double as product sessions; own kits get `track()` | Hand-computed fixtures for every metric (CLAUDE.md test rule); funnels/retention match on a synthetic app dataset |
| **4. Performance** | Sentry transactions + OTLP traces → `spans`, `span_hourly`; transaction list (p50–p99, throughput, error rate), transaction and trace views (waterfall), DB/HTTP span grouping with parameterised descriptions, slow transactions, release-over-release regressions, network failures; errors linked to traces (`trace_id`); web projects link Web Vitals | Percentiles within the histogram error bound against exact values on fixtures; ≥ 10,000 spans/s target |
| **5. Alerting v2** | Rule model (trigger: event/issue/metric/threshold; filters: env, release, platform, level, tags, owner; actions: channel, assign, webhook); routing by ownership; silences and maintenance windows; performance and crash-rate alerts; alert history UI; v1 app kinds migrated to rules | Evaluator unit tests for each trigger; no duplicate fires across restarts |
| **6. Observability** | OTLP logs (search, trace correlation) and metrics (gauges/counters/histograms, rollups), simple dashboards; error → trace → logs navigation | Log search p95 < 1 s on a 7-day, 10 M-row dataset (target) |
| **7. Later** | Profiling, mobile/desktop replay, AI-assisted root cause (summaries over the stored context), MCP write tools | — |

**Cloud and site, in every phase:**
- Cloud: gateway paths for new front doors, quotas per data class (errors, spans, logs, sessions/replays), `CORE_VERSION` bump.
- Site: docs (DSN migration guides "From Sentry" and "From GlitchTip", per-SDK setup), features table, and comparison pages with evidence only.

## 7. API surface

- **Ingest:** Sentry envelope/store, OTLP, our `app-events`.
- **Private (dashboard):** extends `/api/private/sites/{site}/app/...` per phase.
- **Public REST (API keys):**
  - adds `/api/public/v1/sites/{site}/app/...` reads: issues, events, releases, sessions, transactions;
  - scopes: `stats:read` for reads, new **`issues:write`** for status/assign/comment, new **`releases:write`** for the release, deploy and symbol upload API.
- OpenAPI first, with generated clients, as today.

## 8. MCP as a first-class interface

All 7 current web tools stay as they are. Tools ship with the phase that owns their data. They are read-only, with `stats:read`. Write tools later need `issues:write`.

| Phase | Tools |
|---|---|
| 1a | `get_app_overview`, `list_app_issues`, `get_app_issue`, `get_app_compatibility`, `list_app_releases` (the existing plan, kept as-is) |
| 1b | `search_events`, `get_issue_timeline`, `get_issue_impact` (users, sessions, releases, platforms affected); `list_issues` gains the search syntax |
| 2 | `get_release_health`, `compare_releases`, `find_regression_release` |
| 3 | `get_product_overview`, `get_active_users`, `get_retention`, `get_funnel`, `get_event_breakdown`, `get_user_journey` (app-aware wrappers over the web tools) |
| 4 | `get_performance_overview`, `list_slow_transactions`, `get_transaction`, `get_trace` |
| 6 | `search_logs`, `get_trace_logs` |
| 7 | `set_issue_status`, `assign_issue`, `comment_issue` (`issues:write`) |

Every MCP tool output passes the same privacy filter as the API: no hashes, no keys, no unscrubbed text.

## 9. Testing and documentation

- **Golden fixtures** per SDK and item type, recorded in Phase 0. A "compatibility matrix" table in the docs is generated from which fixtures pass.
- **Hand-computed fixtures** for every metric: crash-free, adoption, percentiles, DAU/WAU/MAU, retention.
- **Privacy:** a `pg_dump` scan and an MCP/API output scan per phase.
- **Tenant isolation:** each new endpoint gets the existing matrix test pattern (org, site restriction, scope).
- **Bench gates:** extend `tools/bench` with envelope, span and log generators. Results go to `.workspace/scorecard.md`.
- **Docs:**
  - `docs/guide/apps.md` becomes "App Monitoring";
  - per-SDK setup pages and migration guides;
  - API and MCP reference;
  - a privacy page listing exactly what is kept and what is dropped.

## 10. Decisions needed from the user

| # | Decision | Recommendation |
|---|---|---|
| D1 | Store scrubbed error messages and exception values (reverses the v1 "no free text" anti-goal) | **Yes**, scrubbed by default, with a per-project off switch. Without messages we are not a credible Sentry replacement. |
| D2 | Sentry `user` context | Hash `id`; drop email, username and IP by default; a per-project opt-in keeps `username` only. |
| D3 | App product analytics data path | **A** (reuse web tables), if the Phase 0 spike passes; otherwise B. |
| D4 | Name and navigation: "App Monitoring", next to "Web Analytics" | Yes. Projects keep one table; the kind decides the experience. |
| D5 | Windows/native minidumps in this program | Breakpad `.sym` + minidump in 2d; PDB later. |
| D6 | Cloud pricing: separate quotas per data class, or a single "events" unit | Separate quotas (errors, spans, logs), all features on every plan (consistent with D64). |
| D7 | New Go dependencies for OTLP (protobuf + otlp protos) | Accept (Apache-2.0, needed for compatibility). |

## 11. Risks

- **Postgres ceiling for spans and logs.** Mitigations: short retention, rollups, sampling, and honest positioning ("for teams up to N events/day"; N comes from bench results).
- **Sentry protocol drift.** Mitigations: recorded fixtures per SDK version, and a quarterly fixture refresh.
- **Symbolication size and CPU.** Mitigations: cap debug-file sizes, cache parsed line tables (LRU, memory budget), and keep it a bounded queue so it never blocks ingest.
- **Scope.** Phases 1a–2b are the credible Sentry/GlitchTip alternative. Phases 3–6 widen the product. Each phase must ship usable on its own.
