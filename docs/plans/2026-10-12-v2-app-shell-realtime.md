# Supermetry v2: one design system, an app shell, a realtime engine

Status: **proposed** (2026-10-12). Scope: core dashboard (`web/`), core server (`internal/`), marketing site (`../supermetry-site`).
Goal: from the landing page to the dashboard it looks and feels like one product, the dashboard behaves like a compact desktop app, and live numbers update in about a second instead of polling every 5–30 seconds.

## 1. Where we are (verified 2026-10-12)

| Area | Today | Problem |
|---|---|---|
| Design tokens | Two unrelated systems. Site: `--bg --text --line --violet --font(IBM Plex) --mono` in `site/web/src/styles/global.css`. Dashboard: shadcn names `--background --primary --sidebar-* --chart-1..5`, `--font-sans: system-ui` in `web/src/index.css` (~100 vars). | The landing page's product shot and the real app drift; fonts, indigo, radii, chart colours differ. No test catches drift. |
| Shell | `AppShell.tsx` (209 lines) + `Sidebar.tsx` (377) on shadcn `ui/sidebar`; `useShortcuts` exists. | No command palette, no density setting, full-page navigations for detail views, no `placeholderData` anywhere (0 uses) so filter changes flash empty states. |
| Realtime | Polling only: realtime page 5 s, live summary badge 30 s, live stats 30 s, health 5 s (`useRealtimePage.ts:37`, `useLiveSummary.ts:43`, `lib/stats.ts:152,196`). Server keeps live state in memory (`internal/session`, `internal/realtime/realtime.go`, `RealtimeSnapshot` in `session/store.go:146`). No SSE/WebSocket endpoint exists. | 5–30 s latency, N tabs × M widgets of redundant requests, nothing "feels live". |
| Tests | 149 unit tests, Playwright e2e (`web/e2e/*.spec.ts`), JS size budget 256 KB gzip. | 0 visual-regression screenshots, no token parity or contrast tests, no landing→dashboard journey test. |

Prerequisites from the 2026-10-12 architecture audit (must land first, they are on the v2 path):
P1 default listen `:8787` (setup currently 403s), P2 one `ApiProblem` error model in the web app, P3 writer poison-batch fix (one bad event stops ingest for everyone). Realtime also depends on the audit's multi-instance finding (§4.6).

## 2. Principles

1. **One source of truth for design tokens** (core repo), generated into both codebases; drift fails CI.
2. **The shell never unmounts.** Navigation swaps panels, not pages. Data from the previous view stays visible until the next is ready.
3. **Push, don't poll**, for anything live. Polling remains only as the fallback.
4. **Compact by default, comfortable on request.** Density is a token set, not per-component CSS.
5. Hard rules still hold: Go + PostgreSQL, Redis optional, **no DB read on the ingest hot path**, privacy first (no raw IPs or IDs reach the browser), zero JS by default on the marketing site.

## 3. Design tokens v2 (landing → dashboard)

### 3.1 Source and outputs
- New package `web/packages/tokens/` in the core repo: `tokens.json` in the W3C Design Tokens (DTCG) format, three tiers:
  - **primitive**: `gray.0–12`, `indigo.1–12`, `green/amber/red` scales in OKLCH, font stacks (IBM Plex Sans Variable, IBM Plex Mono), radii, spacing 4px scale, motion durations/easings, shadow = none.
  - **semantic**: `surface.page / surface.raised / surface.sunken`, `text.default / muted / subtle`, `border.hairline / strong`, `accent.default / tint / contrast`, `status.ok/warn/danger`, `chart.series.1–8`, `chart.fill.top/bottom`, `focus.ring`. Light and dark values.
  - **density**: `compact` and `comfortable`: control height (28/32 px), row height (28/36), base text (13/14 px), panel padding (12/16), sidebar width (rail 48, panel 232/256).
- A Bun script `packages/tokens/build.ts` emits:
  - `dist/tokens.css` (CSS custom properties, `:root`, `[data-theme=dark]`, `[data-density=compact]`),
  - `dist/shadcn.css` mapping semantic tokens to shadcn names (`--background: var(--surface-page)` …) so existing components keep working unchanged,
  - `dist/site.css` mapping to the site's current names (`--bg`, `--text`, `--line`, `--violet`, …) so site pages keep working unchanged,
  - `dist/tokens.ts` (typed values for charts/canvas, e.g. Recharts colours, the site's demo engine).
- Dashboard imports `tokens.css` + `shadcn.css` from `index.css`; the hand-written ~100 vars are deleted.
- Site: copies `dist/{tokens,site}.css` into `site/web/src/styles/tokens/` through a sync script pinned to a core release (same model as brand assets: core is the source of truth). Both repos self-host IBM Plex (fontsource, Latin subset).

### 3.2 Migration
1. Generate tokens that reproduce today's dashboard values exactly → swap import → screenshots identical (proves the pipeline).
2. Move values to the site's design world (indigo, Plex, hairlines, sentence case) → review screenshots → accept.
3. Point the site at the generated files; delete duplicated values in `global.css`.
4. The landing `ProductShot` and docs read the same `chart.*` tokens and use the same dataset shape as the app.

### 3.3 Tests (tokens)
- **Parity**: unit test in both repos: generated files are up to date with `tokens.json` (rebuild in CI, `git diff --exit-code`). Site test also checks its synced copy matches the pinned core version.
- **Contrast**: test computes WCAG 2.2 ratios (and APCA Lc as info) for every text/surface and accent/surface pair in light and dark; AA required (4.5:1 body, 3:1 large/UI).
- **Usage lint**: no raw hex/oklch colours in `web/src/**/*.tsx|css` outside `packages/tokens` (Biome/grep rule; allowlist for SVG brand files).
- **Visual regression**: Playwright `toHaveScreenshot` for 8 key screens × light/dark × compact/comfortable (overview, realtime, sessions + session sheet, funnels, settings, login, setup, command palette) with the demo fixture and a frozen clock; site: home, pricing, docs page, compare. Threshold 0.1 %, masks for live regions.

## 4. Realtime engine

### 4.1 Shape
```
ingest handler ─► session store (in memory, per process) ─► realtime hub ─► SSE /api/private/realtime/stream
                                       ▲                         │  per-site 1 s coalescing tick
                     writer / minutes ─┘                         ▼
                                                     browser: one EventSource per tab (leader tab shares via BroadcastChannel)
                                                                 │
                                                     realtime store ─► TanStack Query cache (setQueryData) ─► existing components
```

### 4.2 Server (`internal/realtime`)
- **Hub**: per-site subscriber sets; a ticker (1 s) builds one snapshot per site *only if it has subscribers and changed* (dirty flag set by the session store on update; no DB reads). Snapshot reuses `RichSnapshotWithMinutes`.
- **Payload**: versioned JSON frames: `snapshot` (full, on connect and every 30 s), `delta` (changed fields: active visitors, visitors/minute tail, top pages/sources/countries changes), `event` (privacy-safe live feed: kind, path, source, country, device class, relative time; never visitor IDs, IPs or props values unless the site allows it), `heartbeat` every 15 s. `id:` per frame; `Last-Event-ID` resume sends a fresh snapshot.
- **Endpoint**: `GET /api/private/realtime/stream?site=<id>[&site=…]` (one stream, several sites, for the multi-site overview). Same session auth and the **same org/site scope check** as other private routes (use the audit's `loadScoped` helper, not ad-hoc). Share links get a read-only variant if sharing allows realtime.
- **Limits**: max streams per user (e.g. 6) and per instance (configurable), per-client bounded queue with drop-to-latest (a slow client gets the next snapshot, never blocks the hub), `X-Accel-Buffering: no`, documented proxy settings (nginx/Caddy) in deploy docs.
- **Metrics**: `/metrics` gains streams open, frames sent/dropped, tick duration.

### 4.3 Client (`web/src/features/realtime/engine/`)
- `RealtimeClient`: one `EventSource` per tab; **leader election via BroadcastChannel / Web Locks** so N tabs share one connection; exponential backoff with jitter; pauses on `document.hidden` after 60 s, resumes with resync.
- `realtimeStore` (`useSyncExternalStore`): latest snapshot per site + ring buffer of the last 200 feed events.
- **Query bridge**: applies frames to existing query keys (`liveSummaryKey`, realtime page keys, `stats` live keys) with `setQueryData`, so current components update without a rewrite; the existing `refetchInterval`s become the **fallback** (enabled only when the stream is down for >10 s).
- UI: live badge in the shell (connection state: live / reconnecting / polling), count-up number transitions (respect reduced motion), the realtime page's map/feed animate new items, "N new events" pill on lists instead of shifting rows.

### 4.4 Tests (realtime)
- Go: hub unit tests (coalescing, dirty flags, drop-to-latest, subscriber cleanup on disconnect, scope check denies other org's site), SSE handler test with `httptest` reading frames, resume with `Last-Event-ID`, race detector on.
- Load test (`scripts/` or `tools/bench`): 1 000 concurrent streams × 50 sites at 200 events/s ingest; budgets: p95 frame latency ingest→browser < 1.5 s, hub CPU < 1 core, memory < 50 MB for streams.
- Web: engine unit tests with a fake EventSource (reconnect, leader handover, fallback switch, cache bridge); Playwright: ingest an event via the API, see the live count and feed update within 2 s without a reload; kill the stream → badge shows "polling" and data still updates.

### 4.5 Rollout
Feature flag `SUPERMETRY_REALTIME_STREAM` (default on in dev, then on by default one release later). Polling paths stay as fallback for proxies that buffer SSE.

### 4.6 Multiple instances
Live state is per process today (audit finding). v2 ships **single-instance realtime** and documents it. For Cloud: route ingest per site to one instance (consistent hashing at the proxy) and fan out snapshots between instances with Postgres `LISTEN/NOTIFY` carrying **per-site 1 s aggregates**, never per event. Decide before Cloud launch.

## 5. App shell v2 (compact, app-like)

### 5.1 Layout
- **Top bar 44 px**: org/site switcher (searchable, recent sites), global date range + compare, filter chips, live badge, command palette trigger, account.
- **Left rail**: 48 px icon rail (collapsed) / 232 px panel (expanded), remembered per user; groups: Overview, Realtime, Behaviour (pages, events, journeys, funnels, retention, cohorts), People (sessions, profiles, replays), Revenue, Health; settings at the bottom.
- **Content**: one scroll container; sticky filter bar; panels in a 12-column grid that reflows at 1280 / 1024 / 768 / 390.
- **Detail sheets**: sessions, profiles, replays, funnel steps and link details open in a right sheet (URL-addressable, e.g. `?session=…`), keeping the list and scroll position; full-page view on mobile.

### 5.2 Feel
- **Command palette** (`⌘K` / `Ctrl K`): navigate to any page/site/org, switch date ranges, toggle theme/density, run actions (create goal, copy tracking snippet), search docs. Built on the existing `useShortcuts`; add `g o` (overview), `g r` (realtime), `g s` (sessions), `[`/`]` previous/next period, `/` focus filter, `?` shortcut sheet.
- **No blank states on navigation**: `placeholderData: keepPreviousData` for all report queries; route loaders call `ensureQueryData` (audit web finding 5); skeletons only on first load.
- **Transitions**: View Transitions API for panel/sheet changes (≤ 150 ms, off under reduced motion); optimistic updates for settings toggles.
- **Density**: `data-density` on `<html>` from the token set; tables 28 px rows in compact.
- **Installable**: web app manifest + icons; service worker caches only the shell assets (never API responses), so the app opens instantly; offline shows a clear "offline" state.
- **Errors**: shell-level error boundary per panel (one failing widget never blanks the page), using the P2 `ApiProblem` model.

### 5.3 Performance budgets (CI-enforced)
Initial JS ≤ 256 KB gzip (existing), route chunk ≤ 60 KB, INP p75 < 100 ms and LCP < 1.5 s on the demo dataset (Playwright + web-vitals in the e2e perf lane, `web/e2e/perf`), cached route switch < 100 ms to first paint.

### 5.4 Tests (shell)
Unit: palette command registry, shortcut handling, density/theme persistence. Playwright: keyboard-only navigation through the palette to every top-level page; sheet open/close keeps list scroll; filter change keeps previous data visible (no empty-state flash); a11y audit (existing spec) extended to the palette and sheets; screenshots from §3.3.

## 6. Landing → dashboard journey (one product)

- Same tokens, fonts, icons (Lucide in both; the site renders it via Iconify at build time) and chart styling.
- Product shot on the landing page is generated from the real dashboard components rendered statically (or kept in sync via a screenshot test comparing the shot's layout to the app at the same viewport).
- CTAs: site → `app.supermetry.com/signup` (Cloud) or `/docs/getting-started/install/` (self-host); after setup, onboarding shows the tracking snippet and waits on the **live stream** for the first event ("We received your first pageview"), replacing install-status polling.
- **Journey e2e** (new lane): built site → click "Self-host" / docs install → (core dev server) setup with token → onboarding → send a test event with the npm tracker → live badge shows 1 visitor within 2 s → overview shows the pageview. Runs on CI with the dev Postgres.

## 7. Phases, jobs and acceptance

| Phase | Work | Size | Done when |
|---|---|---|---|
| 0 | Audit prerequisites P1–P3 | S–M | setup works on a fresh `make dev`; poison batch test passes; auth pages show real errors |
| 1 | Token package + dashboard swap at today's values; parity, contrast, usage-lint tests; screenshot baseline | M | screenshots unchanged; tests green |
| 2 | Move to the v2 design values; site consumes generated tokens; Plex in the app | M | reviewed screenshot diffs accepted; site and app share every colour/font/radius |
| 3 | Realtime hub + SSE endpoint + scope checks + metrics + Go tests + load test | L | latency and resource budgets met; race-free |
| 4 | Web realtime engine, query bridge, fallback, live UI touches | M | e2e: event visible < 2 s; fallback e2e passes |
| 5 | App shell: top bar, rail, sheets, palette, shortcuts, density, keepPreviousData, loaders | L | shell tests + a11y + perf budgets green |
| 6 | Manifest/service worker, onboarding first-event via stream, landing product shot sync, journey e2e | M | journey lane green on CI |
| 7 | Multi-instance realtime decision + implementation for Cloud | L | two replicas: no double counts, stream works on both |

Each phase is one Codex job spec (`codex-NN-…`, same format as the site repo's jobs): owned paths, files to read, acceptance commands, commit plan. Phases 1–2 and 3 can run in parallel (different directories); 4 needs 3; 5 needs 1; 6 needs 4 and 5.

## 8. Open decisions for the owner

1. Live feed contents: show page paths and countries by default, or only counts unless the site opts in? (Privacy default suggests counts plus paths, no props values.)
2. Default density for new users: compact (recommended) or comfortable.
3. Multi-instance realtime for Cloud (§4.6): proxy-pinned ingest + `LISTEN/NOTIFY` (recommended, Postgres only) vs optional Redis pub/sub.
4. Product shot: render real components statically on the site (stronger, but couples the site build to the core repo) or keep the hand-built shot with a layout-sync test.

## 9. Decisions (owner approved the plan, 2026-10-12; recommended defaults taken)

1. Live feed: counts plus page paths and countries; never property values or visitor IDs.
2. Default density: compact.
3. Multi-instance realtime for Cloud: proxy-pinned ingest per site plus Postgres `LISTEN/NOTIFY` with 1 s per-site aggregates (no Redis requirement).
4. Landing product shot: keep the hand-built shot, guarded by a layout-sync screenshot test.
