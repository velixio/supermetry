# Demo project: design

Status: accepted (user, 2026-10-10). Plan: [../plans/2026-10-10-demo-project.md](../plans/2026-10-10-demo-project.md).

## 1. Goal

A new user can see every Supermetry feature populated with believable data before installing the tracker.

- After signup, the app offers **"Explore with demo data"**. Skipping is remembered per user and the prompt is not shown again. "Add demo project" stays available in the project switcher and on the empty state.
- The demo is a **dedicated demo project** per organization (at most one live). It never accepts real traffic, and real projects never receive demo data.
- The data looks current: ~90 days of history ending now, plus a **live simulator** so Realtime, today's numbers, replays and vitals keep moving.
- Creating the first real project does **not** delete the demo. A one-time banner offers "Remove demo project". The demo can be removed any time and re-added later (fresh data).

Success: on a fresh demo project every dashboard page and settings list shows data: summary, chart, pages/entry/exit, sources/channels/UTM (all five), geo to city, devices/browser/OS and versions, language, hostname, screen, custom events and props, goals (page, event, scroll, revenue), funnels, revenue, web vitals, journeys, retention, sessions, profiles, heatmaps, session replay, realtime, annotations, segments, and one alert. A test asserts this.

## 2. Decisions

| # | Decision | Why |
|---|---|---|
| 1 | Per-org copy (not one shared demo) | Users can try goals, funnels, alerts, segments on it |
| 2 | 90 days, ~150 visitors/day (~25k events) | Bounded cost per signup; retention class 90 drops old data naturally |
| 3 | Rolling history + live simulator | Realtime and "today" are never empty |
| 4 | Traffic model in `internal/synth` (shared with `tools/gen`) | One model; benchmarks and demo do not drift |
| 5 | In-process writes (writer, session store, blob store), not HTTP ingest | Ingest only accepts timestamps < 24 h old, needs salts, and is rate limited |
| 6 | Demo sites bypass the identity/path privacy gate | Data is synthetic: no real visitor to protect |
| 7 | No auto-delete of the demo | Silent data loss surprises users; banner instead |

Out of scope: a public demo website, email report subscriptions on the demo (would send mail), shared links on the demo, CSV imports, multi-instance realtime for the demo (single instance only; see section 7).

## 3. Data model

Migration `0041_demo_project.sql` (one transaction):

```sql
ALTER TABLE sites ADD COLUMN is_demo boolean NOT NULL DEFAULT false;
ALTER TABLE sites ADD COLUMN demo_seeded_at timestamptz;      -- NULL while seeding
ALTER TABLE sites ADD COLUMN demo_live_through timestamptz;   -- simulator high-water mark
ALTER TABLE sites ADD COLUMN demo_viewed_at timestamptz;      -- last dashboard view (throttled)
CREATE UNIQUE INDEX sites_one_demo_per_org ON sites (org_id) WHERE is_demo AND deleted_at IS NULL;
ALTER TABLE users ADD COLUMN demo_prompt_dismissed_at timestamptz;
```

- `is_demo` is set only by the demo service; `PATCH` of a site never changes it.
- `sites.Site` (internal/sites/registry.go) gains `Demo bool`; `store.ActiveSites` selects `is_demo`; the file registry (`internal/sites/file.go`) gains `"demo"`.
- Demo site settings at creation: name "Demo project", hosts `www.demo.example` and `docs.demo.example` (RFC 2606 reserved), timezone from the request (browser `Intl` zone, default `UTC`), retention 90, revenue currency `USD` (configured), `profiles_enabled` and `heatmaps_enabled` true, replay enabled at 100% sampling. `created_at` is set to the seed start day so the closer's start-day logic covers the history.

## 4. Gates (all fail closed)

1. **Ingest** (`internal/ingest/handler.go`): a hit whose site is `Demo` gets the same response as an unknown site. Count it in the existing drop/invalid metric.
2. **Replay** (`internal/replay/handler.go` Start and Chunk): same as an unknown site.
3. **Writer** (`internal/writer/tracking.go`): `identityAllowed` and `pathsAllowed` return true for a `Demo` site regardless of tracking mode. Everything else (revenue currency check, caps) is unchanged.
4. **Tracker snippet**: the API reports `is_demo`; the UI hides install instructions for demo sites.

## 5. Traffic model (`internal/synth`)

Add an exported, callback-based demo model next to the bench generator. The bench path (`Generator.Run`, NDJSON, truth) must stay byte-identical: `TestFixturesUpToDate` in `tools/gen` is the gate.

```go
// internal/synth/demo.go
type DemoConfig struct {
    Seed     int64
    TZ       *time.Location
    Visitors float64 // mean daily visitors at the end of the window (default 150)
    Anchor   time.Time // seed day (site-local); history shape is fixed relative to it
}
type DemoVisit struct { // one session
    Start      time.Time
    VisitorKey uint64 // stable per synthetic visitor (returning visitors reuse it)
    Host, Referrer, UTMSource, UTMMedium, UTMCampaign, UTMContent, UTMTerm string
    Country, Region, City, Language string
    Browser, BrowserVer, OS, OSVer string
    Device int16; ScreenW int
    Profile *DemoProfile // set for signed-up visitors
    Hits []DemoHit
    Replay bool          // this session gets a recording
}
type DemoHit struct {
    Offset time.Duration
    Kind   int16  // 0 pageview, 1 custom, 2 engagement
    Path, Name string
    Props map[string]string
    RevenueMicros int64 // USD
    EngagementMS int32; ScrollPct int16
    Clicks [][2]int // heatmap clicks for this pageview, viewport-relative
}
type DemoProfile struct { ExternalID string; Traits map[string]string }

func NewDemo(cfg DemoConfig) *Demo
// Visits returns every session that starts in [from, to), in start order.
// Deterministic: same seed and window give the same visits; adjacent windows
// never duplicate or drop a session.
func (d *Demo) Visits(from, to time.Time) []DemoVisit
```

Model ("Acme Cloud", a fictional SaaS with blog, docs and shop):

- **Volume**: growth from ~60% to 100% of `Visitors` over 90 days, weekday/weekend shape, diurnal curve in the site zone, one campaign spike (~3x for 2 days, `utm_campaign=autumn-launch`) ~20 days ago, one dip (~-50% for a day) ~40 days ago.
- **Channels**: direct, organic search (google, bing, duckduckgo), social (x.com, linkedin, reddit), referral (news.ycombinator.com, github.com, chatgpt.com), email (`utm_medium=email`), paid (`utm_medium=cpc` with content and term). All five UTM fields appear.
- **Geo**: weighted countries (US, GB, DE, IN, FR, CA, BR, NL, AU, JP and a tail) with 2–4 region/city pairs each. **Devices**: desktop/mobile/tablet with matching browser, OS, versions and screen widths. **Language** consistent with country. **Hosts**: `www.demo.example` (marketing, shop) and `docs.demo.example`.
- **Pages**: `/`, `/pricing`, `/features/{analytics,replay,funnels}`, `/blog/{5 slugs}`, `/docs/{getting-started,api,tracker}`, `/signup`, `/checkout`, `/checkout/success`, `/account`, plus a broken `/old-pricing` that fires `404`.
- **Events and props**: `Signup` (plan, method), `Purchase` (plan; revenue 19/49/199 USD), `Outbound Link: Click` (url), `File Download` (url), `404` (path), `Form: Submit` (form), `Web Vitals` (lcp, cls×1000, inp within ingest caps), engagement hits with time and scroll.
- **Journeys**: home → pricing → signup → checkout → success funnel with ~40/25/60/80% step conversion; blog readers scroll deep and bounce more.
- **Returning visitors and profiles**: ~30% of visits are returns (same `VisitorKey`) so retention cohorts fill. Visitors who sign up get a `DemoProfile` (`user_<n>`, traits plan, company_size, country) and keep it on later visits.
- **Replay**: `Replay=true` for ~3% of sessions in the last 14 days, max 60 per window.
- **Heatmaps**: clicks on `/`, `/pricing` and `/features/*` pageviews.

`internal/synth/rrweb.go`: `DemoRecording(v DemoVisit) []byte` returns a gzip member of NDJSON rrweb events (meta, a full snapshot of a small templated page for each path, mouse moves, clicks matching `Clicks`, scroll, page navigations), timed to the visit's hits. It must load in the existing replay player (web/src replay route).

## 6. Seeding (`internal/demo`)

`demo.Service` (constructed in `serve.go` with pool, sites service, registry, shared writer, shared session store, replay blob store and index, dictionary, clock):

- `Create(ctx, principal, orgID, tz) (sites.Site, error)`: authz `ActionSiteCreate`; inserts the demo site (409 `demo_exists` if the org has one); audit `site.created` with `demo=true`; invalidates the registry; starts `seed` in a background goroutine tied to the server context; returns immediately.
- `seed(siteID)`:
  1. Window: `[startOfLocalDay(now-89d), now)`.
  2. Uses a **private pipeline** like `cmd/supermetry/load.go`: its own `session.NewMemory`, its own `writer.New`, same dictionary; tracking registry contains the demo site. Feeds `Demo.Visits` in timestamp order: intern dimensions via `dict.IDsForSite`, assign synthetic `VisitorID` from `VisitorKey`, set every `session.Hit`/`writer.Event` field, profiles, revenue, props; heatmap clicks via `EnqueueHeatmap`; for `Replay` visits, `Blobs.Append` + `Index.Add` with `DemoRecording`.
  3. Closes the writer (flush), then `closer.CloseDay` for every past local day in the window.
  4. Creates example settings (section 6.1), sets `demo_seeded_at = now`, `demo_live_through = now`.
  - Target: < 30 s on the dev box for the default volume.
- **Resume on startup**: a demo site with `demo_seeded_at IS NULL` and no seed running is erased (`retention.Erase`, `replay.EraseSite`) and seeded again.
- **Delete**: the normal site delete path (owner/admin). Re-adding creates a new site.

### 6.1 Example settings

Created through the same stores the HTTP handlers use (refactor inline handler SQL into small store functions where needed):

- Goals: `Signup` (event), `Purchase` (event, revenue USD), visit `/pricing` (page), blog scroll 75% (scroll, `/blog/*`).
- Funnel "Signup flow": `/` → `/pricing` → `Signup` → `Purchase`.
- Annotations: campaign start day ("Autumn launch campaign"), dip day ("CDN outage").
- Segments: "Mobile visitors", "Paid search".
- Alert: `traffic_spike`, **disabled**, no channels (nothing is ever sent). Alerts may have zero channels while disabled (migration 0042); enabling still requires a channel. The org's notification channels are never touched.

## 7. Live simulator

Scheduler job `demo-live`, every 15 s:

- For each demo site with `demo_seeded_at` set and `demo_viewed_at > now-3h`:
  - `from = demo_live_through`, `to = now`. If `to - from > 30m`, the gap `[from, to-5m)` is filled through the seeding pipeline (private writer, then close any newly past days); the rest goes live.
  - Live visits go through the **shared** pipeline in time order: `Sessions.Touch`, `RecordActivity` (so Realtime shows them), shared `Writer.Enqueue`, `EnqueueHeatmap`, replay blobs/index. Hits scheduled after `now` are kept for the next tick (a session can span ticks).
  - Update `demo_live_through`.
- `demo_viewed_at` is updated (throttled in memory to once per 5 min per site) by the dashboard query handlers when the site is a demo.
- The scheduler's advisory lock keeps one runner. Realtime for demos is correct on single-instance deploys; on multi-instance deploys only the instance running the job shows demo realtime (documented limitation).

## 8. API

OpenAPI (`internal/api/openapi.yaml`) and private routes:

- `POST /api/private/orgs/{org}/demo` body `{ "timezone": "Europe/Berlin" }` → 202 with the site (`is_demo: true`, `demo_ready: false`). 409 `demo_exists` with the existing site id.
- Site objects gain `is_demo` and `demo_ready` (`demo_seeded_at IS NOT NULL`).
- `GET /api/private/me` (`h.me`) gains `demo_prompt_dismissed: bool`.
- `POST /api/private/me/demo-prompt/dismiss` → 204, sets `demo_prompt_dismissed_at`.
- Not exposed on `/api/public/v1` (demo is a UI onboarding aid).

## 9. Web

- **Onboarding** (`routes/app.index.tsx`): when the org has no sites and `demo_prompt_dismissed` is false, show two choices: "Explore with demo data" (creates the demo, navigates to it) and "Create your first site". "Skip for now" calls dismiss.
- **Switcher** (`components/Sidebar.tsx`): "Add demo project" item when the org has no demo; the demo row shows a "Demo" badge; selecting it opens the dashboard.
- **Demo dashboard**: a slim banner "You're viewing demo data" with "Remove demo project"; while `demo_ready` is false, a "Generating demo data…" state that polls the site every 2 s.
- **Remove banner**: in an org that has a demo and at least one real site, a one-time banner "Your project is ready. Remove the demo project?" (Remove / Keep). Keep is remembered in `localStorage` per org (wrapped in try/catch).
- **Settings**: demo sites hide tracker install and replay/heatmap install steps; show "Demo projects don't accept real traffic."
- Regenerate the API client with `bun run gen:api`.

## 10. Testing

- `internal/synth`: determinism (same seed/window → same visits), adjacent-window partition (no duplicate or missing sessions), shape checks (all channels, UTM fields, event names, revenue, profiles, replay share, vitals within caps); `DemoRecording` decodes as gzip NDJSON with a full snapshot first. `tools/gen` fixtures unchanged.
- Gates: ingest and replay reject a demo `public_id`; writer keeps paths, props, UTMs and profiles for a demo site in anonymous mode and still strips them for a normal site.
- `internal/demo` integration (testpg): seed a demo with a short window (e.g. 3 days, small volume); assert non-empty results from the query layer for every feature listed in section 1 success criteria; idempotent resume after an interrupted seed; delete erases data.
- Live simulator with a fake clock: after a tick, `realtime.RichSnapshot` shows visitors, `demo_live_through` advances, a 2 h gap is backfilled exactly once.
- API handler tests (create 202/409, dismiss, site fields, authz). Web component tests for onboarding, switcher item, banners.

## 11. Amendments (user, 2026-10-11)

1. **Busy only while watched (user, 2026-10-11, revised).** The 90-day history stays quiet (~150 visitors/day). While a **logged-in user has the demo dashboard tab visible**, the simulator keeps **10–20 visitors online** (active within `realtime.LiveWindow`). It follows a gentle daily curve inside that range: about 10 at the site's night, about 20 in the afternoon.
   - The browser sends `POST /api/private/sites/{site}/demo/heartbeat` every 30 s while `document.visibilityState === "visible"`, and immediately when the tab becomes visible. The server treats the demo as **watched** for 60 s after the last heartbeat (in memory, per instance).
   - **Warm start:** when watching begins, the simulator starts sessions that are already in progress (started 1–15 min ago), so Realtime fills at once.
   - **When watching stops** (tab hidden, logged out, or no heartbeat for 60 s), busy traffic stops: pending busy hits are dropped, and only the quiet model continues. Gap fill for unwatched time is quiet. Busy traffic already written stays.
   - Busy sessions: 8–25 min, a pageview every 40–150 s, the usual engagement, event, revenue and vitals odds, and up to 2% replays.
   - Accepted trade-off: today's totals depend on how long the demo was watched.
2. **More variety, everywhere.** About 40 countries, each with 2–5 region/city pairs. A wider spread of browsers and versions, OSes, devices, screen widths, languages, referrers (search, social, news, communities, AI assistants), UTM campaigns and blog/docs pages. All weighted and random per visit, and deterministic per seed.
3. **24-hour lifetime.** A demo project is deleted automatically **24 hours after creation**, with all its data, through the normal site deletion and erasure path. `sites.demo_started_at` (migration 0043) records the real creation time; `created_at` stays backdated for the closer. The API reports `demo_expires_at`, and the UI shows "Demo expires in Xh" and still offers "Remove demo project". After expiry the user can add a new demo; the signup prompt is not shown again.
