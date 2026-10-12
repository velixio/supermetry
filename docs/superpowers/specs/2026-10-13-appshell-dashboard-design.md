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
