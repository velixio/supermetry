# App Health Phases 5–6 brief: app dashboard UI

Spec: `docs/superpowers/specs/2026-11-15-app-health-design.md` §7. The Phase 4 read APIs are under `/api/private/sites/{site}/app/...` (`overview`, `issues`, `issues/{issue}`, `compatibility`, `releases`, and `PATCH issues/{issue}`). Read their OpenAPI entries and the generated types in `web/src/lib/api.gen.ts`. Read the Phase 4 plan file in `docs/superpowers/plans/` for the field names.

Stack and rules: `.claude/CLAUDE.md` Frontend section (Vite, React, TS, TanStack Router and Query, shadcn/ui, Tailwind, shadcn charts; Bun only). Follow the existing pixel dashboard (`web/src/features/pixels/`) and site dashboard patterns for layout, period picker, charts, tables and empty states. Don't add dependencies. The initial JS budget is 256,000 bytes gzipped and the build is currently near 234 KB, so lazy-load the app pages the same way other heavy routes do.

## Phase 5 (commit group 1): overview, issues, issue detail
- **Route `/app/$org/$site`** for an app site: replace `AppEmptyState` with `AppOverview` when the app has events; keep the empty state, with its setup link, when it has none.
  - Header filters: period, release, environment, platform. Keep them in URL search params, using the existing period param conventions.
  - KPI tiles: crash-free users, crash-free sessions (show "n/a" with a tooltip "Send a session event at app start to measure this"), events, crashes, new issues, regressed issues.
  - A daily chart of events and crashes, with a crash-free users line.
  - A "Top issues" table: the first 10 of the issues list.
- **Route `/app/$org/$site/issues`**: the issues table.
  - Status tabs: Open (default), Regressed, Resolved, Ignored, All.
  - Kind filter and search box.
  - Sort by last seen, events or users.
  - Each row shows a sparkline, title, error type, first/last seen, releases, events and users.
  - Cursor or offset paging, as the API provides.
- **Route `/app/$org/$site/issues/$issue`**: the issue detail page.
  - Header: title, kind and status badge.
  - Actions: Resolve, Resolve in next release, Ignore, Reopen. These call the PATCH with an optimistic update, roll back on error, and are hidden for viewers (use the role or capability data the other pages use).
  - Daily chart.
  - Breakdown cards (release, platform, OS, arch, device class, environment, tags) as bar lists.
  - Latest events: expandable rows showing scrubbed frames in a monospace block, plus a copy button.
- **Navigation**: add Issues to the app sidebar (`APP_NAV_SUFFIXES` in `Sidebar.tsx`). Update the sidebar tests.
- **Tests** (vitest plus Testing Library, mocking `privateRequest` the way existing tests do):
  - the overview renders the KPIs from a fixture, and "n/a" when sessions are null;
  - the issues tabs change the request's `status`;
  - the PATCH is optimistic and rolls back on a 500;
  - viewers don't see the actions;
  - the empty state shows when there are zero events.

Commit messages like `apps: overview page`, `apps: issues list`, `apps: issue detail and status actions`.

## Phase 6 (commit group 2): compatibility and releases
- **Route `/app/$org/$site/compatibility`**: a matrix table of platform / OS version / arch / device class with events, crashes, installs, crash-free users (with a colour scale on crash-free, using the existing chart tokens) and the top issues, linked. Platform filter chips.
- **Route `/app/$org/$site/releases`**: releases newest first. Each row shows first/last seen, installs, crash-free users and sessions, and new and regressed issue counts (each linking to the issues list filtered by release).
- Add both to the app sidebar.
- **Setup steps on `/app/$org/sites/new`**: after the key dialog, show a three-step checklist in the dialog or setup page:
  1. add the key to your build;
  2. send a test report, with a `curl` snippet that posts one `kind: "error"` event to `/api/public/v1/app-events`, filling in the app id and a placeholder key `<your key>`;
  3. a "Waiting for first report…" status that polls `overview` every 5 s, for at most 2 minutes, and turns green when `events > 0`.
- Tests for both pages, plus the polling status (use fake timers).

Commit messages like `apps: compatibility matrix`, `apps: releases page`, `apps: test report check on setup`.

## Process
- Write one short plan at `docs/superpowers/plans/2026-11-15-app-health-phase5-6-ui.md` (≤120 lines), commit it, then execute it test-first.
- Do all of Phase 5's commits first. Then create branch `feat/app-health-p6-compat-releases` from that point (`git switch -c`) and do Phase 6 on it.
- Final checks on each branch: `bun run typecheck`, `bun run test`, `bun run build` (the size check must pass).
