# App Health Phases 5-6: dashboard UI

Brief: `2026-11-15-app-health-phase5-6-brief.md` (binding). Spec: `../specs/2026-11-15-app-health-design.md` section 7.

## Shape

All code lives in `web/src/features/apps/`; route files are thin and lazy-loaded by the router's code splitting
(`autoCodeSplitting` is not assumed: pages are imported with `lazy()` in the route component, as other heavy routes do).

- `api.ts`: typed `privateRequest` wrappers and TanStack Query hooks (`useAppOverview`, `useAppIssues`,
  `useAppIssue`, `useAppCompatibility`, `useAppReleases`, `useSetIssueStatus`).
- `search.ts`: URL search params for app pages (`period`, `release`, `environment`, `platform`; issues add
  `status`, `kind`, `q`, `sort`, `offset`), validation, and the mapping to API query strings via `resolveRange`.
- `AppFilters.tsx`: period, release, environment, platform selects (shared by every app page).
- `AppOverview.tsx` (KPI tiles, daily chart, top issues), `IssuesTable.tsx` (tabs, filters, sparkline, paging),
  `IssueDetail.tsx` (header, actions, chart, breakdown bar lists, latest events), `Sparkline.tsx`.
- Phase 6: `CompatibilityMatrix.tsx`, `ReleasesTable.tsx`, `TestReportCheck.tsx` (setup checklist, polling).

## Phase 5 order (test first, one commit per layer)

1. `apps: overview page` - api + search + filters + overview + route + sidebar entry for Issues is added with the issues commit.
2. `apps: issues list` - issues table, route, sidebar item, sidebar tests.
3. `apps: issue detail and status actions` - detail, optimistic PATCH with rollback, viewer gating.

## Phase 6 order (branch `feat/app-health-p6-compat-releases`)

4. `apps: compatibility matrix` (colour scale on crash-free using `--chart-*` tokens, platform chips).
5. `apps: releases page` (links to issues filtered by release).
6. `apps: test report check on setup` (3-step checklist in the key dialog; polls `overview` every 5 s, max 2 min).

## Tests (vitest + Testing Library, `privateRequest` mocked)

Overview KPIs and "n/a" for null sessions; empty state at zero events; status tab changes the request `status`;
PATCH optimistic then rolled back on 500; viewers see no actions; compatibility rows and chips; releases links;
polling with fake timers (turns green, stops after 2 minutes).

## Rulings

- Empty state shows when overview `events` is 0 and no release/environment/platform filter is set.
- Viewer = `membership.role === 'viewer'` (the pattern other pages use).
- "Resolve in next release" sends `resolved_in_release: "next"`.
- Strings are added to `en` only when a locales test demands more; locale files fall back to English.

## Verification

`cd web && bun run typecheck && bun run test && bun run build` on each branch (size check included).
