# App Health Phase 3 brief: app intake, mapping, storage, issues

Spec: `docs/superpowers/specs/2026-11-15-app-health-design.md` (§2, §3, §5, §6, Privacy, "Calls made by Claude"). Read it all first. This brief fixes the open design choices; where the brief and the spec differ, the brief wins.

Already on this branch (Phase 1–2):
- `dropstats.TooOld`.
- Site kind `app`.
- `sites.Site.App *sites.AppConfig` with `Keys`, `TagAllowlist`, `MaxReportAgeDays`, `Environments`, and `KeyValid(hash [32]byte, now) bool`.
- `appkeys.Valid` and `appkeys.Hash`.
- App settings API.
- Ingest keys API.

## Migrations (use numbers 0255–0259 only)

- **`app_events`**: partitioned monthly on `ts` with BRIN on `ts`. Use the partition machinery in `internal/db/partitions.go`, the same way the existing raw events table does: register the table so `EnsurePartitions` and retention `DROP PARTITION` cover it. Columns:
  - `site_id int`
  - `ts timestamptz`
  - `received_at timestamptz`
  - `event_id text`
  - `issue_id bigint`
  - `kind text`, `level text`, `handled bool`
  - `release text`, `build text`, `environment text`
  - `platform text`, `os_version text`, `arch text`, `device_class text`, `runtime text`
  - `error_type text`, `exit_reason text`, `signal text`, `exit_code int`
  - `frames text`
  - `fingerprint text`
  - `occurrences int`
  - `install_hash bytea`, `session_hash bytea`
  - `tags jsonb`

  `kind` has a CHECK on the spec's enum list. No btree indexes beyond what a test query proves it needs; `(site_id, issue_id, ts)` is allowed.
- **`app_issues`**:
  - `id bigint identity pk`
  - `site_id int fk on delete cascade`
  - `fingerprint text`
  - `kind text`, `error_type text`, `title text`
  - `first_seen timestamptz`, `last_seen timestamptz`
  - `first_release text`, `last_release text`
  - `status text CHECK in (open, resolved, ignored, regressed)`, default `open`
  - `resolved_in_release text null`
  - `event_count bigint`
  - `unique(site_id, fingerprint)`
- **`app_hash_secrets`**: `site_id int pk fk on delete cascade`, `secret bytea check(octet_length=32)`, `created_at`. Created lazily on the first event, through the writer (never on the hot path). Loaded into the registry the same way keys are.
- Register every new table in `internal/storageusage/sources.go`. Check that site deletion and org erase cover them; grep how `site_drop_daily` and the events table are handled.

## Endpoint `POST /api/public/v1/app-events` (new package `internal/appingest`)

- **Request shape**: body ≤ 16 KB, `{"site":"<app public id>","events":[…1..20]}`; header `X-Supermetry-Key`. Add the route to the public router and the OpenAPI spec (public section), then regenerate the clients.
- **Request-level checks, in this order**:
  1. A bad method, body size or JSON → 400.
  2. A key that fails `appkeys.Valid` → 401.
  3. An unknown site, or a site whose kind isn't app → 401. Use the same body for both, so the response doesn't reveal which apps exist.
  4. `!site.App.KeyValid(hash, now)` → 401.
  5. More than 60 requests per minute per client IP → 429. Use an in-memory token bucket; never persist the IP.
- **CORS**: answer preflight (`OPTIONS`) allowing `POST`, `Content-Type` and `X-Supermetry-Key` from any origin. The key is the trust signal. No bot filter, no allowed-hosts check.
- **Per-event checks**. Drop reasons form the closed list `too_old`, `invalid`, `tag_not_allowed`, `rate_limited`, `duplicate`; each drop is also recorded in dropstats. Add Reasons `AppInvalid`, `AppDuplicate`, `AppRateLimited` (names `app_invalid`, `app_duplicate`, `app_rate_limited`), plus a CHECK migration, OpenAPI enum and UI labels like Phase 1. Reuse `too_old`.
  - **Field limits**: each field is checked against the spec §2 table. A wrong type, a value over its length, or an unknown enum value → `invalid`.
  - **Timestamps**: `ts` older than `min(App.MaxReportAgeDays, site retention days)` → `too_old`. `ts` more than 5 minutes in the future → clamped to the receive time.
  - **Per-key event budget**: 600 events per minute per key; the excess → `rate_limited`.
  - **Dedup**: by `(site, id)` with an in-memory TTL cache (reuse the `ingest.Dedup` type if it fits) → `duplicate`.
  - **Tags**: if `TagAllowlist` is non-empty, non-allowlisted tags are **removed** and the event is still accepted. The response lists them in `"warnings":[{"index":i,"reason":"tag_not_allowed","tag":"k"}]`. The `tag_not_allowed` drop reason exists but is unused.
  - **Tag cardinality**: at most 1000 distinct values per tag key per app per UTC day, tracked in memory. Further values become `"(other)"`.
  - **Environments**: if `Environments` is non-empty and the event's environment isn't in it, store it as `"(other)"`.
- **Frame scrubbing**, line by line, in a pure function `ScrubFrames(string) string` that is test-first:
  - Replace home folders (`/Users/<x>/`, `/home/<x>/`, `C:\Users\<x>\`, any case) with `~/`.
  - Reduce any other absolute path to its last 3 segments.
  - Remove `file://` prefixes.
  - Cut the result to 4 KB on a line boundary.
- **Fingerprint**: the client's value if it is a valid hex string of ≤ 64 characters. Otherwise `hex(sha256(kind + "|" + error_type + "|" + top 5 normalised frames))`, where normalised means line numbers stripped (`:123`) and lines joined with `\n`.
- **Hashing**: `install_hash` and `session_hash` are `HMAC-SHA256(app secret, value)`. Raw ids are never stored.
- **Reply**: `202 {"accepted":n,"dropped":[{"index","reason"}],"warnings":[…]}`.
- **Hot path**: no DB reads. Accepted events go onto a bounded channel. When the channel is full, the event is dropped with `rate_limited`, a health counter is incremented, and the client still gets 202 with that drop listed.
- **Writer**: a goroutine flushes on size (500) or interval (1 s) and on shutdown, following the existing ingest writer's lifecycle. Each flush:
  1. creates any missing hash secrets;
  2. upserts issues with `last_seen = greatest`, `first_seen = least`, `event_count += sum(occurrences)`, and `last_release`;
  3. regression: a `resolved` issue receiving an event whose release differs from `resolved_in_release`, or any event if that is null, becomes `regressed`;
  4. inserts the events with `CopyFrom`.

  Wire it into `cmd/supermetry` startup and shutdown the same way the existing ingest pipeline is wired.

## VoiceStudio mapping (`internal/appingest/diagnostic_map.go`, the only file with that event's names)

- **Linking**:
  - **Storage**: a web site stores `settings->'app_link'` = the app site's id, on the same org only.
  - **API**: `PUT /api/private/sites/{site}/app-link` `{"app_site_id": int|null}`, owner/admin; 400 if the target isn't an app in the same org.
  - **Registry**: `sites.Site.LinkedAppID int32`.
  - **UI**: in the web site's settings, a select "Send diagnostic reports to app".
- **Mapping in the legacy handler** (`internal/ingest/handler.go`): when `payload.Name == "diagnostic_report"`, the site has `LinkedAppID != 0`, and `identityAllowed` is true:
  - Map the event with spec §3 into an appingest event and enqueue it.
  - Use the **app's** max report age instead of the 24-hour rule.
  - Skip the salt lookup, and don't store it as a custom event.
  - Then return 202.

  The mapping happens after the existing allowed-hosts, strict-origin, bot and dedup checks, and before the 24-hour late-drop check. Unlinked sites, and anonymous or profiles-off sites, behave exactly as today. The existing compat tests must stay green; add linked-site cases.
- `profile_id` → `install_id`. When `category` and `error_code` aren't top-level fields in the new format, they become tags.

## Tests (test-first; real Postgres via `SUPERMETRY_TEST_DSN`)

- **Frames and fingerprints**: `ScrubFrames` and fingerprint unit tests, against hand-computed values.
- **Request checks**: each request-level check returns its status code.
- **Per-event drops**: each drop reason appears in the reply and in dropstats.
- **Tags**: a tag outside the allowlist is stripped, and the reply carries a warning for it. The cardinality cap turns further values into `(other)`.
- **Writer**: it creates an issue and increments it. `first_seen` and `last_seen` hold when events arrive out of order. A resolved issue turns `regressed`.
- **Hashing**: the raw `install_id` never appears in any column; check the stored hash equals the HMAC.
- **VoiceStudio mapping**:
  - a linked site's `diagnostic_report` that is 3 days old is stored in `app_events` with the mapped fields;
  - an unlinked site keeps today's behavior;
  - a link to an app in another org is rejected.
- **Load**: a 20-event batch at 16 KB is accepted. A 16 KB + 1 body → 400.
- **Plumbing**: retention drops old `app_events` partitions, and site deletion cascades to the new tables.

## Process

- Write your own short plan first at `docs/superpowers/plans/2026-11-15-app-health-phase3-intake.md`: a task list with files and test names, following this brief, under 200 lines. Commit it, then execute it.
- Make 6–10 commits split by layer: migrations, appingest core, writer, endpoint, mapping, link API, UI, docs.
- Each commit builds, and `go vet` passes.
- Add a section on the new endpoint to `docs/guide/apps.md`.
