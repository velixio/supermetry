# AppShell and dashboard redesign: specification

Status: **proposed** (2026-10-13). Scope: `web/` only. No backend change.
Builds on `docs/plans/2026-10-12-v2-app-shell-realtime.md` section 5 (shell v2), which sets the direction (44 px top bar, icon rail, one scroll container, detail sheets, palette). This spec turns that direction into steps against the current code and adds what that plan leaves out: a shared page frame, breadcrumbs, and a navigation cleanup. Tokens (plan section 3) and realtime (section 4) are out of scope here.

## 1. Current-state audit (verified 2026-10-13)

| Area | Today | Problem |
|---|---|---|
| Shell | `AppShell.tsx` on shadcn `ui/sidebar`: skip link, `SupermetrySidebar`, `SidebarInset` with `ProjectTabs` (sticky h-9), `<header>` (sticky h-14), `<main class="p-4 md:p-6">`, footer. `AccountFrame.tsx` is a second, sidebar-less shell for `/app/account` and `/app/admin`. | Two stacked sticky bars (tabs + header) cost 92 px. Account/admin pages feel like another app. |
| Header | Sidebar trigger, site icon and name, live badge, help, theme, Settings and Account menus. | No breadcrumbs, search, or notifications. Site name is repeated in the header, the sidebar dropdown and the dashboard subtitle. |
| Navigation | Sidebar groups Analyze, Behaviour, Conversion, Manage (`Sidebar.tsx` `navigationSections`). Settings live only in `HeaderMenus`. | Project switching exists in three places (tabs, sidebar dropdown, "Add site"). `navigationLink` repeats one default-search object in 12 cases. Settings are not discoverable from the sidebar. |
| Page frame | No `PageHeader` or `PageContainer`. Each page hand-rolls its `<h1>` as `text-xl`, `text-2xl`, with or without `tracking-tight`; containers use `gap-4`, `gap-5` or `gap-6`; `main` has no max width. | Visible inconsistency page to page. |
| Dashboard | `routes/app.$org.$site.index.tsx`: h1, subtitle, switcher, manage, imported checkbox; then export plus filters row; no-data banner; stats strip; chart; deferred below-fold block (min-h 3000 px). | Three rows of chrome before data. Loading and anonymous states render an sr-only h1 plus a card. |
| Design system | Tailwind v4, oklch tokens, light and dark, shadcn components (25). Missing: `breadcrumb`, `command`, `avatar`, `scroll-area`, `collapsible`, `kbd`. | Needed primitives are not installed. |
| i18n | `messages/en.ts` and `messages/ui.ts`, read at render time. Several hardcoded English strings (ProjectTabs, AccountFrame, parts of Sidebar and the dashboard). | New code must not add more. |
| Shortcuts | `d` (theme) and `?` (help) in the shell; `m` and `c` on the dashboard; Ctrl/Cmd+B toggles the sidebar. | The help dialog lists only `d` and `?`. |
| Tests | `AppShell.test`, `sidebar-navigation.test`, `sidebar-height.test`, `spa-navigation.test`, `HeaderMenus.test`, `shortcuts.test`, `theme.test`, `router.test`, e2e `navigation`, `account-navigation`, `privacy-navigation`, `dashboard`, `ui-qa`, `a11y-audit`, `not-found`, `sites-overview`. | All must stay green. |


## 2. Proposed layout

```text
┌────────┬─────────────────────────────────────────────────────────────┐
│ ▣ Logo │ ☰  Org / Site / Page        [Search ⌘K]   ● 12 live  ?  ☾  👤│  44 px, sticky
│ ────── ├─────────────────────────────────────────────────────────────┤
│ Site ▾ │ Page title                                  [Primary action]│  PageHeader
│        │ description                                                 │
│ Analyze│ ┌ filters: date range · compare · filter chips ┐ sticky      │
│  Dash  │ └──────────────────────────────────────────────┘             │
│  Events│                                                             │
│  ...   │                      PageContainer                          │
│ Behav. │                  (one scroll container)                     │
│ Conv.  │                                                             │
│ ────── │                                                             │
│ Manage │                                                             │
│ Settings                                                             │
│ ⚙ User │                                                             │
└────────┴─────────────────────────────────────────────────────────────┘
 Mobile (<768): sidebar becomes a sheet via the ☰ trigger; breadcrumbs collapse to the current page; filters scroll horizontally.
```

Structure changes, all on the existing shadcn sidebar:

1. **Remove `ProjectTabs`.** The sidebar header becomes the single project and org switcher. This is the only switcher; the "Add site" item stays inside it.
2. **One header, 44 px**: sidebar trigger, `Breadcrumbs`, search/command trigger, live badge, help, theme, notifications only if a real source exists (see section 9). Settings and Account move to the sidebar footer. `HeaderMenus` is reused there, not rewritten.
3. **`PageHeader` and `PageContainer`** adopted by every route, so title size, spacing and max width come from one place.
4. **`AccountFrame` becomes a layout over the same shell** (sidebar with account/admin items) so there is one shell.
5. **Sidebar**: collapses to an icon rail (shadcn `collapsible="icon"`), cookie persistence kept, Settings added as a group at the bottom.

## 3. Navigation architecture

Keep the existing groups and routes. Do not add items.

- **Site scope**: Analyze (Dashboard, Events, Sessions, Realtime, Replays when allowed), Behaviour (Journeys, Retention, Profiles, Cohorts, Heatmaps, Vitals), Conversion (Funnels, Goals, Revenue), Manage (Links).
- **Org scope**: Sites, Health (owner and admin), Org settings.
- **Settings**: a "Settings" group at the bottom: Site settings (non-viewers) and Org settings, linking to the existing settings layouts. Account and Admin live in the footer user menu.
- **Active state**: use the existing `matchesSite` and `activeSite`; the matching is unchanged.
- **Breadcrumbs**: `Org / Site / Section / Detail`. Org and site are links; the last crumb is the page. Derived from the route match plus a small `routeCrumbs` map, with no per-page code. On mobile only the last two show.
- **Default search**: replace the 12 repeated default-search objects with one `defaultSearch()` helper (refactor, no behaviour change).
- **Keyboard**: Tab order is skip link, sidebar, header, main. Ctrl/Cmd+B toggles the sidebar (existing). `⌘K` opens the palette (step 6). Focus rings come from the existing tokens. The shortcut help dialog is corrected to list every registered key.

## 4. Visual system

Use the existing tokens. Do not introduce new colours here; token v2 (plan section 3) will re-skin everything later.

- **Type**: page title `text-xl font-semibold tracking-tight` everywhere (the dominant existing style). Section titles `text-sm font-medium`. Body `text-sm`.
- **Spacing**: `PageContainer` uses `gap-4`, `p-4 md:p-6`, `max-w-screen-2xl` for analytics pages and `max-w-5xl` for settings and account via a `width` prop.
- **Radius and elevation**: existing `--radius`, no shadows beyond the card default.
- **Icons**: lucide, 16 px in nav and header, 14 px inline.
- **States**: reuse `Skeleton`, `Empty`, `ErrorState`, `EmptyState`, `NoDataBanner`. Add no new patterns.
- **Themes**: light and dark already exist; the toggle stays.

## 5. Dashboard redesign

Content stays the same, with no invented metrics. Only the arrangement changes:

1. The h1 moves into `PageHeader`: title "Dashboard", description is the date range. The site name leaves the dashboard because it is already in the breadcrumbs.
2. `PageHeader` actions: DashboardSwitcher, DashboardManage, Export. The "include imported" checkbox moves into the filter row.
3. One sticky filter row: DatePicker, compare, FilterBar, include-imported.
4. Order below it is unchanged: no-data banner, stats strip, chart, below-fold.
5. The loading, anonymous and unknown-policy states use the same `PageHeader`, with no sr-only h1 hack.
6. Edit mode and custom dashboards (`DashboardEditor`, `DashboardViewer`) are untouched apart from sitting inside the new frame.

## 6. Responsive behaviour

- **>=1280**: expanded sidebar (16 rem), full breadcrumbs.
- **768-1279**: sidebar collapsed to the icon rail by default (cookie wins if set); filters wrap.
- **<768**: sidebar is a sheet (existing `useIsMobile`); header shows trigger, last two crumbs, search icon, live badge; filters scroll horizontally; tables keep their existing horizontal scroll.

## 7. Component breakdown

Follow the repo's PascalCase files in `web/src/components/`, no new directories.

| Component | Action |
|---|---|
| `AppShell` | Modify: drop `ProjectTabs`, new header, `PageContainer` inside `main`. |
| `SupermetrySidebar` (`Sidebar.tsx`) | Modify: icon-rail mode, Settings group, user footer; extract `defaultSearch()`. Name stays. |
| `AppHeader` | New, extracted from the header block in `AppShell` (about 40 lines). |
| `Breadcrumbs` plus `routeCrumbs.ts` | New; uses shadcn `breadcrumb` (install). |
| `PageContainer`, `PageHeader` | New; adopted route by route. |
| `AccountFrame` | Modify: render inside the shared shell. |
| `ProjectTabs` | Delete after the switcher consolidation, with its tests moved. |
| `CommandPalette` | New in step 6, using shadcn `command` (install). |
| `MobileNavigation` | Not needed: the shadcn sidebar sheet already does this. |
| `HeaderMenus` | Reuse in the sidebar footer. |

Dependencies: shadcn `breadcrumb` and `command` (`cmdk`); every other primitive is already installed. `cmdk` is the only new package, and it is for step 6 only.

## 8. Implementation plan (ordered, each step ships alone and passes the gate)

Branch `feat/appshell-frame`, one PR per step, with no push to main. Tests first where behaviour changes.

1. **Frame primitives**: `PageContainer`, `PageHeader` with unit tests. Adopt on the dashboard only. Strings go into `messages/ui.ts`.
2. **Adopt everywhere**: swap hand-rolled h1 blocks for `PageHeader` across the routes the audit lists (about 20 files, mechanical); no visual change except consistency.
3. **Breadcrumbs**: install `breadcrumb`, add `routeCrumbs`, add `Breadcrumbs`, wire into the header. Tests for crumbs on site, org and settings routes.
4. **Header and switcher consolidation**: extract `AppHeader`, remove `ProjectTabs`, move Settings and Account to the sidebar footer, add the Settings group. Update `AppShell.test`, `sidebar-navigation.test`, `HeaderMenus.test`, e2e navigation specs.
5. **Sidebar rail and responsive**: icon-rail collapse, tablet default, fix the help dialog's shortcut list. Update `sidebar-height.test` and add a rail test.
6. **Dashboard layout**: restructure per section 5 and add the sticky filter row. Update `-dashboard-breakdowns.test` and `dashboard.spec`.
7. **AccountFrame onto the shell**: with the `account-navigation.spec` update.
8. **Command palette**: separate PR, per plan section 5.2, after steps 1-7 land.

**Validation per step**: `bun run typecheck`, `bun run test`, `bun run build` (JS budget 256 KB gzip), Playwright `navigation`, `dashboard`, `a11y-audit`, `ui-qa`; manual keyboard-only pass; screenshots at 1440, 1024, 390 in light and dark.

**Regressions to watch**: the single `main` landmark after the skip link; the sticky offsets (the `top-9` offset disappears with the tabs); the last-site recording; the anonymous-tracking route blocking; the `sidebar_state` cookie; privacy-navigation e2e.

## 9. Acceptance criteria

- Every `/app/...` route renders inside one shell, including account and admin.
- Exactly one sticky bar above content, 44 px high.
- Every page uses `PageHeader` and `PageContainer`; no hand-rolled h1 remains in routes.
- Breadcrumbs are correct on 8 sampled routes (dashboard, funnel detail, profile detail, link detail, site settings, org settings, health, account).
- Project/org switching exists in exactly one place.
- No visible control without behaviour; no fabricated metric; no new hardcoded English.
- Keyboard-only navigation reaches every nav item and the header actions with visible focus; the a11y audit has no new violations.
- 390 px: no horizontal page scroll; the sidebar opens as a sheet.
- All tests listed in section 1 pass and the JS budget holds.

## 10. Risks, dependencies, unresolved questions

- **Overlap with the v2 plan** (section 5, still "proposed"): the palette, density, detail sheets and token swap stay there. This spec covers only the frame. If the owner prefers to do the v2 shell in one go, steps 1-5 here fold into that job.
- **Blast radius**: step 2 touches about 20 route files; keep it mechanical and one PR.
- **Test churn**: `ProjectTabs` removal changes several tests and e2e specs.
- **Unresolved: notifications.** There is an org notifications settings route, but no inbox. I will not add a bell without a real source.
- **Unresolved: global search.** I propose it only as the command palette (step 8) and do not add a decorative search box earlier.

## 11. Addendum: dashboard as main panel plus inspector (user choice, 2026-10-13)

Chosen from three original layouts (main plus inspector, stacked resizable panes, three-pane workspace). The other two are rejected: stacked panes hide cards behind tabs and the three-pane workspace needs a wide screen. This section extends sections 5 and 6; where they conflict, this section wins.

### 11.1 Behaviour

- The dashboard body is a two-panel group inside `PageContainer`: **main** (stats strip, chart, card grid; scrolls on its own) and an **inspector** (closed by default).
- The inspector opens when the user chooses **Inspect** on a row in a `BreakdownCard`, a country in the Where card, or a cell in Weekly rhythm (chart-point inspection was dropped, see 11.7). Clicking a row still applies the filter as today; Inspect is a separate, labelled control (the "View full report" and details dialogs stay reachable from inside the inspector).
- Content is what the current queries already return for that selection: the value's own metrics (the same metrics the card shows) for the active date range and filters, its top breakdown by the other dimensions where the API supports it, and the actions "Add as filter" and "Open full report". **No new metrics, no new endpoints** (web-only scope holds).
- Closing: Esc, the close button, or selecting the same row again. Opening another row replaces the contents and keeps focus management consistent.
- Width: default 24 rem, resizable 20-32 rem with a keyboard-operable handle, remembered per browser in `localStorage` (try/catch, renders without it). Main keeps a minimum of 40 rem; below that the inspector overlays as a sheet.

### 11.2 State and URL

The selection lives in the route search params as `inspect=<dimension>:<value>` (validated in the route's `validateSearch`, unknown dimensions dropped). This gives deep links, back/forward, and survival across date and filter changes. Changing a filter that excludes the value shows an "Out of range" empty state in the inspector rather than closing it.

### 11.3 Responsive

- **>=1280**: side-by-side panels.
- **768-1279**: inspector overlays main as a right-hand `Sheet` (not a push layout); the sidebar is already on the icon rail.
- **<768**: inspector is a bottom `Sheet`, full width, 85% height.

### 11.4 Components and dependencies

| Component | Action |
|---|---|
| `DashboardPanels` | New: owns the panel group, width persistence and the sheet fallback via `useIsMobile` and a width query. |
| `Inspector` | New: header (dimension, value, close), body (metrics, breakdowns, actions), loading and error states reusing `Skeleton` and `ErrorState`. |
| `BreakdownCard`, Where, Weekly rhythm (not `MainChart`, see 11.7) | Modify: add the Inspect control and expose `onInspect(dimension, value)`. |
| `routes/app.$org.$site.index.tsx` | Modify: `inspect` search param, render `DashboardPanels`. |

New dependency: shadcn `resizable` (`react-resizable-panels`). Decide at step 6 whether it fits the JS budget (256 KB gzip); if not, use a fixed-width inspector with no drag handle and drop the resize criterion below.

### 11.5 Plan additions

Insert after step 6, as its own PRs:

- **6a. Inspector shell**: `DashboardPanels`, `Inspector`, `inspect` search param, close and focus behaviour, sheet fallbacks. Tests first: URL round trip, Esc closes, focus returns to the Inspect control, overlay below 1280.
- **6b. Wire the sources**: add Inspect to `BreakdownCard`, then Where, Weekly rhythm and the chart, one PR each if the diff grows.

### 11.6 Acceptance criteria (added)

- Inspect opens the inspector from each of the three sources (Breakdown rows, Where countries, Weekly rhythm cells) without changing the applied filters.
- Reloading a URL with `inspect=` restores the same selection; back closes it.
- Focus moves into the inspector on open and returns to the invoking control on close; the inspector is a labelled region (`aside` with a heading), and the single `main` landmark rule still holds.
- Keyboard-only: open, resize (if enabled), close, with visible focus.
- At 390 px there is no horizontal page scroll and the inspector is a bottom sheet.
- Every number in the inspector comes from an existing API response; none is derived client-side except formatting.
- Playwright `dashboard`, `a11y-audit` and `ui-qa` pass, plus a new `inspector` spec.

### 11.7 Risks and open points

- **Which dimensions support a cross-breakdown** depends on the query API; step 6a must list them from `internal/api/openapi.yaml` before building the body, and show only what exists.
- **Chart point inspection: dropped (2026-10-13).** `MainChart` fetches one metric per bucket from `/stats/timeseries`, and buckets may be hours, weeks or months. A point's own metrics would need a new per-bucket query, so the inspector opens from breakdown rows, Where countries and Weekly rhythm cells only. Weekly rhythm cells show only the cell value.
- **Interaction with edit mode and custom dashboards**: the inspector is off while `DashboardEditor` is active.
