# App Health Phase 2: App Site Kind, Ingest Key, Settings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An org owner can create an **App** (desktop, mobile, backend) on `/app/{org}/sites/new`, receive a write-only ingest key shown once, rotate it with a 7-day grace, and edit per-app settings; the in-memory registry exposes all of it for the Phase 3 intake.

**Architecture:** `kind='app'` joins `web`/`pixel` on `sites`. Ingest keys live in a new `app_ingest_keys` table (sha256 only). App settings live in `sites.settings->'app'` (jsonb, same pattern as `settings->'replay'`). The registry refresh loads both into `sites.Site.App`, so Phase 3 verifies keys with no DB read. Two small private endpoints (keys, app settings) avoid touching the positional `UpdateSite` SQL.

**Tech Stack:** Go (net/http, pgx), Postgres, OpenAPI codegen, React + TanStack Router + shadcn (Bun).

**Spec:** `docs/superpowers/specs/2026-11-15-app-health-design.md` (§1, "Calls made by Claude": ingest key; Delivery plan item 2; "Visible progress").

## Global Constraints

- Go binary + Postgres; no DB read on the ingest hot path (registry holds key hashes).
- Forward-only transactional migrations; numbers `0253`, `0254` (0252 is Phase 1).
- Ingest key: header `X-Supermetry-Key`, value `smk_` + lower-case base32 (no padding) of 24 random bytes; only `sha256(plaintext)` is stored; rotation keeps the old key valid for **7 days**.
- Max report age: integer days, default **7**, min 1, max **30**.
- An app has a name, timezone, retention, and **no domain/hosts**; `allowed_hosts` is `{}`.
- Kind is fixed at creation.
- Owner/admin only for key issue/rotate and app-settings writes (same role gate as `strict_origin` in `internal/platform/http/handlers_sites.go:~347`).
- Frontend: Bun only; regenerate clients with `cd web && bun run gen:api`; Go types via the repo's existing openapi Go generator (find it: `grep -rn 'oapi-codegen\|openapi.gen.go' Makefile internal/api/*.go`).
- Commits: identity velixio, no trailers, no AI mentions; scan staged diff for emails.
- UI copy: plain, no VoiceStudio names.

## Review Focus

1. An app site must never get a domain requirement: creating with `hosts` omitted on a managed instance (`requireDomain`) must succeed (pin in Task 1).
2. Updating an app's name/timezone/retention via the existing PATCH must not fail with "at least one host is required" (`sitesvc/sites.go` Update branches ~343/352/445; pin in Task 1).
3. Rotating twice within 7 days: the first key's grace must not be extended, the newest key has no expiry, and at most the current key plus keys still in grace stay valid (pin in Task 2).
4. Web UI receiving `kind: "app"` in places that only knew web/pixel (`AppShell.tsx:90`, `app.$org.$site.index.tsx:91-95`) must not loop fetching or crash (pin in Task 5).
5. App settings PUT with duplicates or mixed case in lists: normalised (lower-cased, deduped, sorted) rather than rejected; over-limit lists rejected with 400 (pin in Task 3).

---

### Task 1: `app` kind in DB, core, create/update, API enum

**Files:**
- Create: `internal/db/migrations/0253_site_kind_app.sql`: drop `sites_kind_check`, re-add as `CHECK (kind IN ('web','pixel','app'))`.
- Modify: `internal/sites/registry.go:11-21` (`KindApp = "app"`; comment on `Kind`).
- Modify: `internal/platform/sitesvc/sites.go:165-188` (Create `case KindApp`: same as pixel: validation host `"app.invalid"`, `hosts = []string{}`, currency `""`, state unconfigured; default error `"kind must be web, pixel or app"`), and the Update/retention branches at ~343, ~352, ~445 (treat app like pixel).
- Modify: `internal/api/openapi.yaml:4952, 5023` (enums add `app`; CreateSiteRequest description: hosts and revenue_currency ignored for pixel and app); regenerate Go + TS clients.
- Modify: `internal/testapi/testapi.go`: add `func (e *Env) AppSite(t *testing.T, org Org) Site` (kind app, UTC, retention 90).
- Test: `internal/platform/http/handlers_sites_test.go`.

**Interfaces:**
- Produces: `sites.KindApp`; `testapi.(*Env).AppSite`.

- [ ] **Step 1: Failing tests** (model on `TestCreateSiteAcceptsPixelKind`, line 27):
  - `TestCreateSiteAcceptsAppKind`: POST `/api/private/sites` `{"name":"Desk","kind":"app","timezone":"UTC","retention_days":90}` → 201, body `kind=="app"`, `allowed_hosts==[]`, DB row `kind='app'`.
  - `TestCreateAppSiteIgnoresRequireDomain`: same with the service built with domain required → 201.
  - `TestUpdateAppSiteNameWithoutHosts`: PATCH name/timezone on the app site → 200.
  - `TestCreateSiteRejectsUnknownKind`: `"kind":"desktop"` → 400 (OpenAPI enum or service message).
- [ ] **Step 2:** Run `go test ./internal/platform/... -run 'AppKind|AppSite|UnknownKind' -count=1` with the DSN; expect FAIL.
- [ ] **Step 3:** Implement as listed. Do **not** add a kind check to `internal/ingest`: app sites have no allowed hosts, so the legacy `/events` already rejects them (`host_not_allowed`); add one assertion for that to `TestCreateSiteAcceptsAppKind` only if a handler fixture is at hand, otherwise skip.
- [ ] **Step 4:** Tests pass; `go build ./... && go vet ./...`; `go test ./internal/platform/... ./internal/db/... -count=1`.
- [ ] **Step 5: Commits:** `db: allow app site kind`, `sites: create and update app sites`, `api: app site kind`.

### Task 2: Ingest keys (issue, rotate, list) loaded into the registry

**Files:**
- Create: `internal/db/migrations/0254_app_ingest_keys.sql`:
  `app_ingest_keys(id bigint generated always as identity primary key, site_id integer not null references sites(id) on delete cascade, prefix text not null check (char_length(prefix)=8), hash bytea not null unique check (octet_length(hash)=32), created_by bigint null, created_at timestamptz not null default now(), expires_at timestamptz null)` + index `(site_id)`. (`created_by` type must match `users.id`; check it.)
- Create: `internal/platform/appkeys/appkeys.go`:
  - `func New() (plain string, prefix string, hash [32]byte, err error)`: `plain = "smk_" + base32lower(24 random bytes)`, `prefix = plain[4:12]`, `hash = sha256(plain)`.
  - `func Hash(plain string) [32]byte`.
  - `func Valid(plain string) bool`: `smk_` + exactly 39 chars of `[a-z2-7]`.
- Create: store functions in `internal/platform/store/app_keys.go`:
  - `func (s *Store) RotateAppKey(ctx, siteID int32, createdBy *int64, prefix string, hash [32]byte, now time.Time) (previousExpiresAt *time.Time, err error)` in one tx: delete rows with `expires_at <= now`; set `expires_at = now + 7 days` on rows where `expires_at is null`; insert the new row; return the grace end if a row was demoted.
  - `func (s *Store) ListAppKeys(ctx, siteID int32) ([]AppKeyRow, error)` → prefix, created_at, expires_at; excludes rows with `expires_at <= now()`.
  - `func (s *Store) ActiveAppKeys(ctx) ([]AppKeyRow /* with SiteID, Hash */, error)` → all rows with `expires_at is null or expires_at > now()`.
- Modify: `internal/sites/registry.go`: `type AppConfig struct { Keys []AppKey; TagAllowlist []string; MaxReportAgeDays int; Environments []string }`, `type AppKey struct { Hash [32]byte; ExpiresAt time.Time /* zero = no expiry */ }`, `Site.App *AppConfig` (nil unless kind app), and `func (c *AppConfig) KeyValid(hash [32]byte, now time.Time) bool` (constant-time compare per key, expiry checked against `now`).
- Modify: `internal/platform/sitesvc/registry.go` refresh: after `loadReplaySettings`, load `ActiveAppKeys` and attach to app sites (`MaxReportAgeDays` default 7 until Task 3 fills settings).
- Modify: `internal/platform/http`: routes `POST /api/private/sites/{site}/ingest-keys` (201 `{"key","prefix","previous_expires_at"}`) and `GET /api/private/sites/{site}/ingest-keys` (200 `{"keys":[{"prefix","created_at","expires_at"}]}`); 404 when the site isn't kind app (pattern: `pixelSite` in `handlers_pixels.go:37`); owner/admin; audit row `site.ingest_key_rotated` (follow how `site.created` audits); call registry `Invalidate()` after rotate. Add both to `openapi.yaml` (operationIds `rotateAppIngestKey`, `listAppIngestKeys`); regenerate. Extend the Mutations interface only if the handler pattern requires it, and update every fake listed by `grep -rn 'noopSiteMutations\|noopMutations\|IconMutations' internal/platform/http/*_test.go`.
- Test: `internal/platform/appkeys/appkeys_test.go`, `internal/platform/store/app_keys_test.go`, `internal/platform/http/handlers_app_keys_test.go`, `internal/sites/registry_test.go` (or nearest).

**Interfaces:**
- Consumes: `sites.KindApp`, `testapi.AppSite` (Task 1).
- Produces: `sites.Site.App *sites.AppConfig`, `(*AppConfig).KeyValid(hash [32]byte, now time.Time) bool`, `appkeys.Hash(plain) [32]byte`, `appkeys.Valid(plain) bool` (Phase 3 uses all of these).

- [ ] **Step 1: Failing tests:**
  - `TestNewKeyFormat`: `Valid(plain)`, `len(plain)==43`, `prefix==plain[4:12]`, `hash==sha256(plain)`; two calls differ.
  - `TestValidRejects`: `""`, `"smk_"`, uppercase, wrong prefix `pnl_…`, 38/40 chars, `'1'`/`'8'` (not base32).
  - `TestRotateAppKeyGrace`: rotate at t0 → `previous_expires_at` nil; rotate at t0+1h → first key `expires_at == t0+1h+7d`, returned value equal; rotate at t0+2h → first key's expiry unchanged (t0+1h+7d), second key gets t0+2h+7d; at t0+1h+7d+1s a rotate deletes the first row.
  - `TestKeyValid`: current key valid; grace key valid before expiry, invalid at/after; unknown hash invalid; nil `*AppConfig` → false.
  - HTTP: POST on app site as owner → 201 with key matching `Valid`; key not present in any later GET body; POST on a web site → 404; as viewer/member → 403; after POST, `registry.ByPublicID(...).App.KeyValid(appkeys.Hash(key), now)` is true without manual refresh.
- [ ] **Step 2:** Run focused tests; expect FAIL.
- [ ] **Step 3:** Implement as listed.
- [ ] **Step 4:** Focused + `go test ./internal/platform/... ./internal/sites/... ./internal/db/... -count=1` pass; build/vet clean.
- [ ] **Step 5: Commits:** `db: app ingest keys table`, `appkeys: generate and verify ingest keys`, `sites: load app ingest keys into the registry`, `api: issue and rotate app ingest keys`.

### Task 3: App settings (tag allowlist, max report age, environments)

**Files:**
- Create: `internal/sites/appsettings.go`: `type AppSettings struct { TagAllowlist []string \`json:"tag_allowlist"\`; MaxReportAgeDays int \`json:"max_report_age_days"\`; Environments []string \`json:"environments"\` }`, `func ParseAppSettings(raw []byte) (AppSettings, error)` (empty/missing → defaults), `func (a AppSettings) Normalize() (AppSettings, error)`.
  - Tag key: `^[a-z][a-z0-9_.]{0,39}$`, ≤ 50 entries. Environment: `^[a-z0-9][a-z0-9_.-]{0,31}$`, ≤ 20 entries. Lists lower-cased, trimmed, deduped, sorted before validation. `MaxReportAgeDays` 0 → 7; outside 1..30 → error.
- Modify: store: `func (s *Store) AppSettings(ctx, siteID int32) ([]byte, error)` and `SetAppSettings(ctx, siteID int32, raw []byte) error` (`settings = jsonb_set(settings, '{app}', $2::jsonb, true)`); `ActiveSites` query also returns `s.settings->'app'` (only that column added).
- Modify: `sitesvc/registry.go` + `coreSite`: fill `Site.App.TagAllowlist/MaxReportAgeDays/Environments` from parsed settings for app sites.
- Modify: http: `GET /api/private/sites/{site}/app-settings` (200 AppSettings) and `PUT` same path (body AppSettings, 200 normalised result; 400 on invalid with the error message; 404 non-app; owner/admin; audit `site.app_settings_changed`; `Invalidate()`). OpenAPI schema `AppSettings` (operationIds `getAppSettings`, `updateAppSettings`); regenerate.
- Test: `internal/sites/appsettings_test.go`, `internal/platform/http/handlers_app_settings_test.go`.

**Interfaces:**
- Consumes: Task 2's `sites.AppConfig`, `Site.App`.
- Produces: `sites.AppSettings`, `sites.ParseAppSettings`; registry `Site.App` fully populated.

- [ ] **Step 1: Failing tests:**
  - `TestParseAppSettingsDefaults`: `nil`/`{}` → `{[], 7, []}`.
  - `TestNormalize`: `["Source","source"," stage "]` → `["source","stage"]`; envs `["Beta","production","beta"]` → `["beta","production"]`; age 31 → error; age 0 → 7; 51 tags → error; tag `"9x"` → error; 21 envs → error.
  - HTTP: PUT then GET round-trips the normalised value; PUT on web site → 404; viewer → 403; invalid → 400; registry reflects new `MaxReportAgeDays` right after PUT.
- [ ] **Step 2:** Run; FAIL. **Step 3:** Implement. **Step 4:** Focused + package tests pass.
- [ ] **Step 5: Commits:** `sites: app settings model and validation`, `api: read and update app settings`.

### Task 4: Create an App from `/sites/new` (and onboarding), key shown once

**Files:**
- Modify: `web/src/features/sites/types.ts:11-33` (`SiteKind` adds `'app'`; `AppSiteInput = { kind: 'app'; name: string; timezone: string; retention_days: number }`; add to `SiteInput`), `web/src/lib/me.ts:11`.
- Modify: `web/src/features/sites/KindChoice.tsx`: third option `{ kind: 'app', title: "App", hint: "Desktop, mobile or backend app: crashes, errors and compatibility." }` (put strings in the same `en` object the other options use); grid `sm:grid-cols-3`.
- Create: `web/src/features/sites/AppProjectForm.tsx` (template: `PixelProjectForm.tsx`): name, TimezoneSelect, retention select (same options as `SiteForm`; default 90); submits `AppSiteInput`.
- Create: `web/src/features/sites/AppKeyDialog.tsx` (template: `features/settings/ApiKeySecretDialog.tsx`): props `{ appId: string; ingestKey: string; onClose: () => void }`; shows the key read-only with copy button, the line "Shown once. Store it in your app's build configuration.", and a snippet:
  ```
  SUPERMETRY_APP_ID=<appId>
  SUPERMETRY_INGEST_KEY=<ingestKey>
  # header on every report: X-Supermetry-Key: <ingestKey>
  ```
- Modify: `web/src/features/sites/api.ts`: `rotateAppIngestKey(siteId, orgId)` → `POST /api/private/sites/{site}/ingest-keys` (use the path param style other per-site private calls use).
- Modify: `web/src/routes/app.$org.sites.new.tsx` and `web/src/features/sites/Onboarding.tsx`: `kind === 'app'` renders `AppProjectForm`; on submit: `createSite` → `rotateAppIngestKey` → open `AppKeyDialog`; on close navigate to `/app/$org/$site/settings/app` (route from Task 5; until Task 5 lands, navigate to `/app/$org/$site`).
- Test: `web/src/features/sites/AppProjectForm.test.tsx` (pattern: `KindChoice.test.tsx`, which tests Onboarding).

- [ ] **Step 1: Failing tests:** choosing radio `/App/` then submitting name "Desk" posts `{kind:'app',name:'Desk',timezone:<default>,retention_days:90}`, then posts to `…/ingest-keys`; the dialog shows the returned key and the app public id; closing navigates; failure of the key call shows an error and a "Create key" retry button (the site already exists).
- [ ] **Step 2:** `cd web && bun run test -- AppProjectForm KindChoice`; FAIL. **Step 3:** Implement. **Step 4:** tests + `bun run typecheck` pass.
- [ ] **Step 5: Commit:** `sites: create apps from the new site page`.

### Task 5: App settings page, key rotation, app-aware navigation

**Files:**
- Create: `web/src/routes/app.$org.$site.settings.app.tsx` + `web/src/features/settings/AppSettingsPage.tsx`: form for tag allowlist (comma/line separated), max report age (number 1–30), environments; Save → PUT app-settings, shows server-normalised values; "Ingest keys" card: list (prefix `smk_<prefix>…`, created, "valid until <date>" for grace keys), "Rotate key" button with confirm text "The current key keeps working for 7 days." → POST → `AppKeyDialog`.
- Modify: settings tabs (`web/src/features/settings/SiteSettings.tsx`): for app sites show only General (name/timezone/retention), App, and Danger zone (whatever existing tabs cover delete); find tab ids there.
- Modify: `web/src/components/Sidebar.tsx:88-113`: `APP_NAV_SUFFIXES = ['', '/sites', '/health', '/settings']`, replays off for app.
- Modify: `web/src/components/AppShell.tsx:90`, `web/src/routes/app.$org.$site.index.tsx:91-95`: treat `'app'` as a known kind; app index renders `AppEmptyState` (create `web/src/features/apps/AppEmptyState.tsx`: "No reports yet", link "Set up this app" → settings/app). `web/src/lib/switchTarget.ts:60`: app like pixel.
- Modify: Task 4's navigation target to `/app/$org/$site/settings/app`.
- Test: `AppSettingsPage.test.tsx`, extend `sidebar-navigation.test.tsx` and `switchTarget.test.ts`.

- [ ] **Step 1: Failing tests:** settings page loads GET values, Save sends PUT with parsed lists and shows normalised response; Rotate confirms then shows dialog; sidebar for `kind:'app'` shows exactly the `APP_NAV_SUFFIXES` items; switchTarget returns false for app; index route for an app site renders "No reports yet" and makes no `getSite` fetch beyond the normal one (assert the mock call count).
- [ ] **Step 2:** Run; FAIL. **Step 3:** Implement (`bun run gen:routes` for the new route). **Step 4:** tests + typecheck pass.
- [ ] **Step 5: Commit:** `apps: settings page, key rotation and navigation`.

### Finish

- [ ] `go build ./... && go vet ./...`; `go test ./internal/... -count=1` (known unrelated failure: `TestPrivateAnalyticsMatrixValidMalformedAndTenantBoundaries`); `cd web && bun run build`.
- [ ] Push `feat/app-health-p2-app-kind`, PR based on `feat/app-health-p1-intake`.
- [ ] Visible progress: rebuild `./bin/supermetry` from this branch and restart the running instance (ask the user before the first restart), then load `/app/yupcha/sites/new` and check the App option renders.
