# Dashboard Filter Row and Inspector Implementation Plan (step 6, 6a, 6b)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the dashboard one sticky filter row (date range with compare, filters, include imported), and add a "main panel plus inspector" layout. In that layout an Inspect control on a breakdown row, a Where country or a Weekly rhythm cell opens a side inspector. It is deep-linkable through `inspect=<dimension>:<value>`, and it shows only numbers that existing queries return.

**Architecture:** The dashboard route keeps `PageHeader` (h1 "Dashboard", Switcher, Manage and now Export in its actions) and gains a `role="group"` filter row with `sticky top-11`. A pure module `lib/inspect.ts` owns the `inspect` search value: parse, serialise, the privacy check and the cross-breakdown list. The route validates the value in `validateSearch`, and `activeInspectTarget()` decides whether it applies (standard dashboard only, anonymous-safe only). `DashboardPanels` lays out main and inspector. At 1280 px and wider it uses side-by-side shadcn `resizable` panels, with the width remembered in `localStorage`. Below 1280 px it uses a right `Sheet`, and below 768 px a bottom `Sheet`. `Inspector` renders the body. It reuses the `StatsStrip` totals query and the `CountryDetails` breakdown pattern with the value added as an `is` filter, so it adds no new endpoint or metric. The sources pass an `InspectControl` down to `InspectButton`s.

**Tech Stack:** React 19, TypeScript, TanStack Router (search params) and Query, shadcn/ui (Radix `Sheet`, new `resizable` on `react-resizable-panels@4.11.2`), Tailwind v4, Vitest and Testing Library, Playwright (edited, not run), Bun.

**Spec:** `docs/superpowers/specs/2026-10-13-appshell-dashboard-design.md`: sections 5, 6, 9 and 10, and all of section 11 (section 11 wins on conflicts). Plan format follows `docs/superpowers/plans/2026-10-13-appshell-shell-4-5.md`. Out of scope: `AccountFrame` (step 7) and the command palette (step 8).

## Global Constraints

- Scope is `web/` only. No backend change, no new endpoint, no new metric. Every number in the inspector comes from `POST /api/private/query` or `GET /api/private/stats/breakdown`, the same calls the dashboard already makes. Only formatting happens client-side.
- Use Bun only: `bun install --frozen-lockfile`, `bun add`, `bun run <script>`, `bunx`. Never use npm. Run commands from `web/`.
- `bun run build` enforces the initial-JS budget of 256000 bytes gzip (`web/scripts/size-check.mjs`). It was 217932 bytes at plan time (commit `5d468c37`). The only new package allowed is `react-resizable-panels@4.11.2`, and it stays only if the build passes. Otherwise apply **Fallback F** (end of this plan).
- No new hardcoded English. New strings go in `web/src/messages/ui.ts` under `inspector`, with the same keys in `web/src/messages/locales/de.ui.ts` and `es.ui.ts`. `web/src/messages/locales/locales.test.ts` enforces key, type and function-arity parity. Read strings at render time, never at module scope.
- Use shadcn primitives (`Button`, `Sheet`, `Skeleton`, new `ui/resizable.tsx`), existing tokens and lucide icons. Do not edit `web/src/components/ui/sheet.tsx` or `ui/sidebar.tsx`.
- Exactly one `main` landmark (`#main-content` in `AppShell`). The side-by-side inspector is an `aside` labelled by its `h2`. The sheet inspector is a Radix dialog named by its `SheetTitle`.
- Search params: the `inspect` value is validated only in the dashboard route's `validateSearch` (`routes/app.$org.$site.index.tsx`). Other routes that call `parseDashboardSearch` must not gain it. Unknown or malformed values are dropped.
- Privacy: in anonymous tracking mode, an inspect target is honoured only when `isAnonymousDimensionAllowed(dimension)` and `isAnonymousFilterAllowed({dimension, operator: 'is', values})` both hold (`web/src/lib/tracking-policy.ts`). Weekly rhythm cells are the exception: they show a value the grid already displays. Cross-breakdowns in anonymous mode use only `channel`, `country` and `device`. No Inspect control appears where the row has no filter action today (`allowFilter=false`, empty label, place without country). Route-level blocking (`anonymousRouteRestricted`, `AnalyticsPrivacyBoundary`) is untouched: the inspector lives inside the dashboard route, which is not a restricted path.
- The inspector is off while `DashboardEditor` is active and on saved dashboards (`DashboardViewer`). There it ignores any `inspect` value and renders no Inspect controls.
- Widths: the inspector defaults to 24rem and resizes from 20rem to 32rem with a keyboard-operable handle. The width is stored in `localStorage` under `supermetry:inspector-width`, and every access is wrapped in try/catch. Main keeps at least 40rem. Responsive: at 1280 px and wider the panels sit side by side; from 768 to 1279 px the inspector is a right `Sheet` 24rem wide; below 768 px it is a bottom `Sheet` at 85dvh.
- Commit on the current branch `feat/appshell-steps-4-8`. Do not create branches and do not push. Identity: `velixio <270455167+velixio@users.noreply.github.com>`. Commit messages describe the change only: no trailers, no tool or assistant names.
- Before every commit, scan the added lines for email addresses: `git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'`. Fixture addresses already used by tests (`owner@example.com`) are placeholders. Stop and ask the human about any other match.
- Playwright cannot run in the execution environment, because the e2e webServer fails on DB login. Every behaviour is pinned by Vitest and Testing Library. E2e files listed below are **edited, not run**. Say so in commit or PR text and never claim they pass.

## Review Focus

- **A deep link whose value the current range or filters exclude** (for example `inspect=country:IN` with no Indian traffic in range). The inspector stays open and shows "Out of range" instead of closing or showing zeros. Pinned in Task 5 (`Inspector` out-of-range test, value and traffic-cell variants).
- **Esc inside a dialog opened from the inspector** ("View full report"). It closes only that dialog, not the inspector. Pinned in Task 4 (an aside Esc handler that ignores already-handled events).
- **An anonymous-tracking site opened with a crafted `inspect=page:%2F` or an unknown label** (`country:ZZ`). Nothing opens and no query with that filter is sent. Pinned in Task 2 (`inspectAllowed` and `activeInspectTarget` tables) and Task 6 (route test asserting no `page` filter request).
- **A dashboard that already has 10 filters.** Inspecting an 11th value would make `/api/private/query` reject the request (`QueryRequest.filters maxItems: 10`), so the inspector says why and sends nothing. Pinned in Task 5.
- **`localStorage` blocked, empty or holding garbage**, plus page paths containing `,`, `:` or `/`. The inspector still opens at 24rem and the value round-trips through the URL. Pinned in Task 3 (width storage) and Task 2 (parse and serialise table).

---

## Decisions taken in this plan (from the spec plus code; no open product choice)

1. **Export moves into the `PageHeader` actions** (spec section 5.2). The filter row order is DatePicker (which already contains Compare), then FilterBar, then the include-imported checkbox (section 5.3).
2. **Below 768 px the filter row keeps the existing `ResponsiveFilters` sheet**, not horizontal scrolling. Section 6 says "filters scroll horizontally", but the sheet already exists and is pinned by `ResponsiveFilters.test.tsx` and by `e2e/polish.spec.ts:39-43`, which already expects the include-imported checkbox inside the "Filters" sheet. A horizontal strip would remove a tested behaviour for no gain.
3. **What the inspector body shows**, listed from `internal/api/openapi.yaml` before building it (spec section 11.7):
   - Value metrics: the exact request `StatsStrip` sends (`web/src/components/StatsStrip.tsx:115-121`): `POST /api/private/query` with metrics `visitors, visits, pageviews, views_per_visit, bounce_rate, visit_duration, engagement_time`, no dimensions and no compare. The inspected value is appended as `{dimension, operator: 'is', values}`. These are the numbers the strip shows after "Add as filter".
   - Cross-breakdowns: `GET /api/private/stats/breakdown` with `metrics=visitors`, `limit=5` and one dimension from `StatsBreakdownDimension` (openapi `components.schemas.StatsBreakdownDimension`), filtered by the value. This is the pattern `CountryDetails` already uses (`web/src/features/map/CountryDetails.tsx:40-63`). Identified sites use `page`, `source`, `country` and `device`; anonymous sites use `channel`, `country` and `device`. The inspected dimension is skipped, as is `country` for region and city, and `page` for entry and exit.
   - Filterable inspect dimensions are exactly the `BreakdownCard` tab dimensions in `DashboardBelowFold.tsx:72-107`. All of them are in the `Filter.dimension` list (`web/src/lib/search.ts:191-218`), and region and city use the `[place, ISO country]` pair (openapi `Filter.values`).
   - Weekly rhythm (`weekday_hour`): the frontend has no filter dimension for weekday or hour (`search.ts` `dimensions` lacks them). The cell inspector therefore reads only the cell value from the grid response (`GET /stats/breakdown?dimension=weekday,hour_of_day&limit=168`, cached from `WeeklyRhythmCard.tsx:11`). It has no "Add as filter", no full report and no cross-breakdown.
   - "Open full report" reuses `FullReportDialog` with a dimension query that has the same shape as `BreakdownCard`'s `cardQuery` (`BreakdownCard.tsx:66-78`). "Add as filter" calls the route's existing `onFilter`.
4. **Chart point inspection is dropped.** `MainChart` fetches `GET /stats/timeseries` with one metric per bucket (`MainChart.tsx:297-305`), and buckets may be hours, weeks or months. A point's own metrics would need a new per-bucket query, which the dashboard does not make today, and hour buckets cannot be expressed as a `date_range`. Spec section 11.7 allows dropping it. Task 9 records this in the spec.
5. **`resizable` decision:** the dashboard route is code-split (`vite.config.ts` `autoCodeSplitting: true`), so `react-resizable-panels` lands in the route chunk, not in the initial JS that `size-check.mjs` counts. Task 6 measures it. If `Initial JavaScript` exceeds 256000, apply Fallback F (fixed 24rem inspector, no handle, no width storage). A plan-time probe confirmed that `react-resizable-panels@4.11.2` renders `Group`, `Panel` and `Separator` in jsdom with a `ResizeObserver` stub, and that its sizes accept `rem` strings (`SizeUnit = "px" | "%" | "em" | "rem" | "vh" | "vw"`).

---

## File Structure

Line numbers refer to commit `5d468c37`. Earlier tasks shift them, so find each spot by the quoted code.

Create:
- `web/src/lib/inspect.ts`: the `InspectTarget` model, `parseInspect`, `serializeInspect`, `parseInspectSearch`, `withoutInspect`, `sameInspect`, `nextInspect`, `inspectTargetFor`, `inspectFilter`, `inspectAllowed`, `activeInspectTarget`, `inspectCrossDimensions`, `InspectControl`.
- `web/src/lib/inspect.test.ts`
- `web/src/lib/breakdown-labels.ts`: `breakdownLabel` (moved out of `BreakdownCard.tsx` so `Inspector` does not pull the card into the route chunk).
- `web/src/components/inspectorWidth.ts`, `inspectorWidth.test.ts`: width clamp and storage.
- `web/src/components/ui/resizable.tsx`: the shadcn resizable primitive (v4 API).
- `web/src/hooks/use-min-width.ts`: viewport width query.
- `web/src/components/DashboardPanels.tsx`, `DashboardPanels.test.tsx`
- `web/src/components/Inspector.tsx`, `Inspector.test.tsx`
- `web/src/components/InspectButton.tsx`
- `web/e2e/inspector.spec.ts` (edited, not run)

Modify:
- `web/src/routes/app.$org.$site.index.tsx`: filter row, Export in the header, `inspect` param, panels, inspect control.
- `web/src/components/ResponsiveFilters.tsx`: optional `className`.
- `web/src/components/BreakdownCard.tsx`: `inspect` prop and Inspect buttons; `breakdownLabel` moves out.
- `web/src/components/DashboardBelowFold.tsx`: passes `inspect` through.
- `web/src/components/StatsStrip.tsx`: export `statMetrics` and `metricValue`.
- `web/src/features/map/trafficGrid.ts`: gains `hourLabel` (moved from `TrafficGrid.tsx`).
- `web/src/features/map/TrafficGrid.tsx`, `WeeklyRhythmCard.tsx`, `WhereCard.tsx`, `CountryTable.tsx`: `inspect` prop.
- `web/src/messages/ui.ts`, `web/src/messages/locales/de.ui.ts`, `es.ui.ts`: `inspector` strings.
- `web/package.json`, `web/bun.lock`: `react-resizable-panels@4.11.2`.
- Tests: `web/src/routes/-dashboard-breakdowns.test.tsx`, `web/src/components/ResponsiveFilters.test.tsx`, `web/src/components/sticky-offsets.test.ts`, `web/src/components/breakdown-card.test.tsx`, `web/src/features/map/CountryTable.test.tsx`, `web/src/features/map/TrafficGrid.test.tsx`.
- E2e (edited, not run): `web/e2e/dashboard.spec.ts`.
- Spec note: `docs/superpowers/specs/2026-10-13-appshell-dashboard-design.md` section 11.7.

Unchanged and checked: `e2e/polish.spec.ts` (already expects the checkbox in the Filters sheet at 390 px and on the page at 1440 px), `e2e/full-report.spec.ts`, `e2e/dashboard-where-when.spec.ts` (selectors are not renamed; the Inspect labels start with "Inspect", so `/^India/` and the `'Details'` substring selectors in `dashboard.spec.ts:49,99` still match only the old controls).

Baseline, recorded at plan time (`5d468c37`): `bun run typecheck` passes; `bun run test` gives 164 files passed and 1 failed, 988 tests passed and 1 failed. The failure is the pre-existing `packages/tokens/test/tokens.test.ts > contain the current semantic values from tokens.json`, which this plan does not touch. Every "all tests pass" step below means "no failure except that one".

```bash
cd web && bun install --frozen-lockfile && bun run typecheck && bun run test 2>&1 | grep -E "FAIL|Test Files|Tests "
```

---

## Group 6: the sticky filter row

### Task 1: One sticky filter row, with Export in the header actions

**Files:**
- Modify: `web/src/routes/app.$org.$site.index.tsx:164-225`, `web/src/components/ResponsiveFilters.tsx`
- Test: `web/src/routes/-dashboard-breakdowns.test.tsx`, `web/src/components/ResponsiveFilters.test.tsx`, `web/src/components/sticky-offsets.test.ts`
- E2e (edited, not run): `web/e2e/dashboard.spec.ts`

**Interfaces:**
- Produces: `ResponsiveFilters({ children, className? }: { children: ReactNode; className?: string })`. `className` is merged onto the desktop wrapper only. The dashboard filter row is `<div role="group" aria-label={en.polish.filters} className="sticky top-11 z-10 bg-background py-2">`.

- [ ] **Step 1: Write the failing tests**

Append inside `describe('dashboard breakdown route', ...)` in `web/src/routes/-dashboard-breakdowns.test.tsx`, after the existing `renderDashboard` function:

```tsx
  it('puts the date range, segments and include-imported in one sticky filter row and Export in the header actions', async () => {
    renderDashboard()
    const row = await screen.findByRole('group', { name: 'Filters' })
    expect(row).toHaveClass('sticky', 'top-11', 'z-10', 'bg-background')
    const date = within(row).getByRole('button', { name: /date range/i })
    const segments = within(row).getByRole('button', { name: 'Segments' })
    const imported = within(row).getByRole('checkbox', { name: 'Include imported data' })
    expect(within(row).getByRole('combobox', { name: 'Compare' })).toBeVisible()
    expect(date.compareDocumentPosition(segments) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(segments.compareDocumentPosition(imported) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    const actions = document.querySelector('[data-slot="page-header-actions"]') as HTMLElement
    expect(within(actions).getByRole('button', { name: 'Export CSV' })).toBeVisible()
    expect(within(actions).queryByRole('checkbox')).toBeNull()
    expect(within(row).queryByRole('button', { name: 'Export CSV' })).toBeNull()
  })
```

Append to `web/src/components/ResponsiveFilters.test.tsx`, inside the `describe` block:

```tsx
  it('lets a page lay the desktop controls out from the start', () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1280 })
    Object.defineProperty(window, 'matchMedia', { configurable: true, value: vi.fn().mockReturnValue({ addEventListener: vi.fn(), removeEventListener: vi.fn() }) })
    render(<ResponsiveFilters className="justify-start"><button type="button">Date range</button></ResponsiveFilters>)

    const wrapper = screen.getByRole('button', { name: 'Date range' }).parentElement as HTMLElement
    expect(wrapper).toHaveClass('flex', 'flex-wrap', 'justify-start')
    expect(wrapper).not.toHaveClass('justify-end')
  })
```

In `web/src/components/sticky-offsets.test.ts`, extend `EXPECTED`:

```ts
const EXPECTED = [
  'src/components/AccountFrame.tsx: sticky top-0',
  'src/components/AppHeader.tsx: sticky top-0',
  'src/components/FullReportDialog.tsx: sticky top-0',
  'src/features/settings/SettingsPages.tsx: lg:sticky lg:top-17',
  'src/routes/app.$org.$site.index.tsx: sticky top-11',
]
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && bun run test -- src/routes/-dashboard-breakdowns.test.tsx src/components/ResponsiveFilters.test.tsx src/components/sticky-offsets.test.ts`
Expected: FAIL. The route test fails with "Unable to find role="group" and name "Filters"". The ResponsiveFilters test fails because the wrapper has `justify-end`. The sticky test fails because the route entry is missing. All existing tests pass.

- [ ] **Step 3: Accept a desktop `className` in `ResponsiveFilters`**

In `web/src/components/ResponsiveFilters.tsx`, add `import { cn } from '@/lib/utils'` after the `useIsMobile` import, and replace the signature and desktop return:

```tsx
export function ResponsiveFilters({ children, className }: { children: ReactNode; className?: string }) {
  const mobile = useIsMobile()
  if (!mobile) return <div className={cn('flex flex-wrap items-center justify-end gap-2', className)}>{children}</div>
```

The mobile branch is unchanged.

- [ ] **Step 4: Restructure the header actions and the filter row**

In `web/src/routes/app.$org.$site.index.tsx`, replace everything from `        actions={` (line 164) to the end of the filter row `      </div>}` (line 225) with:

```tsx
        actions={
          <>
            <Suspense fallback={null}>
              <DashboardSwitcher
                siteId={site.id}
                value={search.dash}
                onChange={(key, dashboard) => openDashboard(key, dashboard)}
                onDefault={(key, dashboard) => openDashboard(key, dashboard, true)}
              />
            </Suspense>
            {membership?.role !== 'viewer' ? (
              <Suspense fallback={null}>
                <DashboardManage
                  canWrite
                  currentId={dashboardId}
                  editing={edit !== null}
                  onEdit={(id) => setEdit({ kind: 'edit', id })}
                  onNew={(template) => setEdit({ kind: 'new', template })}
                />
              </Suspense>
            ) : null}
            {edit ? null : <DashboardExportMenu query={query} anonymous={anonymous} />}
          </>
        }
      />
      {edit ? null : (
        <div aria-label={en.polish.filters} className="sticky top-11 z-10 bg-background py-2" role="group">
          <ResponsiveFilters className="justify-start">
            <DatePicker
              search={search}
              labels={en.dashboard}
              onChange={updateSearch}
            />
            {anonymous ? <div className="flex flex-wrap items-center gap-2 text-sm">
              <p className="text-muted-foreground">Anonymous analytics: filter by a country, channel, browser, OS, or device row. Saved segments and free-text filters are unavailable.</p>
              {filters.map((filter, index) => <Button key={index} variant="outline" size="sm" onClick={() => updateSearch(removeFilter(search, index))} aria-label={`Remove ${filter.dimension} filter`}>{filter.dimension} {filter.operator} {filter.values.map((value) => filter.dimension === 'country' ? countryName(value) : value).join(', ')} ×</Button>)}
              {dropped.length > 0 ? <p role="status">{en.dashboard.invalidFilter}</p> : null}
            </div> : <FilterBar
              siteId={site.id}
              orgId={membership?.org.id}
              canEdit={membership?.role !== 'viewer'}
              dateRange={query.dateRange}
              filters={filters}
              dropped={dropped}
              invalidNotice={en.dashboard.invalidFilter}
              filterLabel={(filter) => `${filter.dimension} ${filter.operator} ${filter.values.map((value) => filter.dimension === 'country' ? countryName(value) : value).join(', ')}`}
              removeLabel={(filter) =>
                en.dashboard.filterLabel
                  .replace('{dimension}', filter.dimension)
                  .replace('{value}', filter.values.map((value) => filter.dimension === 'country' ? countryName(value) : value).join(', '))
              }
              onApply={(segmentFilters) =>
                updateSearch(replaceFilters(search, segmentFilters))
              }
              onAdd={onFilter}
              onRemove={(index) => updateSearch(removeFilter(search, index))}
            />}
            <Field orientation="horizontal" className="w-auto min-h-9 gap-2">
              <Checkbox id="dashboard-include-imported" checked={search.include_imported} onCheckedChange={(checked) => updateSearch({ ...search, include_imported: checked === true })} />
              <FieldLabel htmlFor="dashboard-include-imported" className="text-sm text-muted-foreground">{en.polish.includeImported}</FieldLabel>
            </Field>
          </ResponsiveFilters>
        </div>
      )}
```

The anonymous block's existing English strings move unchanged. No new strings are added: `en.polish.filters` is `'Filters'` (`web/src/messages/en.ts:295`). Everything below (no-data banner, editor, viewer, standard branch) is untouched, so the order below the row is unchanged.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd web && bun run test -- src/routes/-dashboard-breakdowns.test.tsx src/components/ResponsiveFilters.test.tsx src/components/sticky-offsets.test.ts`
Expected: PASS, including the existing `clicking a country row adds the is filter to router search` (it still finds the `Compare` combobox) and the CSV export tests (the `Export CSV` button moved but keeps its name).

- [ ] **Step 6: Edit the e2e (edited, not run)**

In `web/e2e/dashboard.spec.ts`, inside `test('compare, CSV, theme persistence, and the M shortcut work', ...)`, insert directly after `await page.goto(dashboard)`:

```ts
  const filterRow = page.getByRole('group', { name: 'Filters' })
  await expect(filterRow.getByRole('checkbox', { name: 'Include imported data' })).toBeVisible()
  await expect(filterRow.getByRole('combobox', { name: 'Compare' })).toBeVisible()
```

- [ ] **Step 7: Full check and commit**

Run: `cd web && bun run typecheck && bun run test 2>&1 | grep -E "FAIL|Test Files|Tests "`
Expected: typecheck passes; the only failure is the baseline tokens test.

```bash
git add web/src/routes/app.\$org.\$site.index.tsx web/src/components/ResponsiveFilters.tsx web/src/components/ResponsiveFilters.test.tsx web/src/components/sticky-offsets.test.ts web/src/routes/-dashboard-breakdowns.test.tsx web/e2e/dashboard.spec.ts
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+' || true
git commit -m "feat(web): move dashboard filters into one sticky row and Export into the header actions"
```

---

## Group 6a: the inspector shell

### Task 2: The `inspect` search value

**Files:**
- Create: `web/src/lib/inspect.ts`, `web/src/lib/inspect.test.ts`
- Modify: `web/src/routes/app.$org.$site.index.tsx:64-71`
- Test: `web/src/routes/-dashboard-breakdowns.test.tsx`

**Interfaces:**
- Consumes: `Filter` (type) from `@/lib/search`; `isAnonymousDimensionAllowed` and `isAnonymousFilterAllowed` from `@/lib/tracking-policy`.
- Produces (all exported from `@/lib/inspect`):
  - `inspectDimensions` (readonly tuple); `type InspectDimension`; `type InspectTarget = { dimension: InspectDimension; values: string[] }`.
  - `inspectTargetFor(dimension: string, values: string[]): InspectTarget | undefined`
  - `parseInspect(raw: unknown): InspectTarget | undefined`
  - `serializeInspect(target: InspectTarget): string`
  - `parseInspectSearch(raw: unknown): { inspect?: string }`
  - `withoutInspect<T extends { inspect?: string }>(search: T): Omit<T, 'inspect'>`
  - `sameInspect(left?: InspectTarget, right?: InspectTarget): boolean`
  - `nextInspect(current: InspectTarget | undefined, target: InspectTarget): string | undefined` (`undefined` means close)
  - `inspectFilter(target: InspectTarget): Filter | undefined` (`undefined` for `weekday_hour`)
  - `inspectAllowed(target: InspectTarget, anonymous: boolean): boolean`
  - `activeInspectTarget(raw: unknown, context: { anonymous: boolean; standardDashboard: boolean }): InspectTarget | undefined`
  - `inspectCrossDimensions(target: InspectTarget, anonymous: boolean): string[]`
  - `type InspectControl = { selected?: InspectTarget; can(target: InspectTarget): boolean; open(target: InspectTarget, origin: HTMLElement): void }`
  - The dashboard route's search type becomes `DashboardSearch & { inspect?: string }`.

- [ ] **Step 1: Write the failing unit tests**

Create `web/src/lib/inspect.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import {
  activeInspectTarget,
  inspectAllowed,
  inspectCrossDimensions,
  inspectFilter,
  inspectTargetFor,
  nextInspect,
  parseInspect,
  parseInspectSearch,
  sameInspect,
  serializeInspect,
  withoutInspect,
  type InspectTarget,
} from './inspect'

const india: InspectTarget = { dimension: 'country', values: ['IN'] }

describe('inspect search value', () => {
  it.each([
    ['country:IN', { dimension: 'country', values: ['IN'] }],
    ['page:%2Fpricing%2Cplans%3Aannual', { dimension: 'page', values: ['/pricing,plans:annual'] }],
    ['city:Springfield,US', { dimension: 'city', values: ['Springfield', 'US'] }],
    ['weekday_hour:1,13', { dimension: 'weekday_hour', values: ['1', '13'] }],
    ['screen_size:mobile', { dimension: 'screen_size', values: ['mobile'] }],
  ])('parses and re-serialises %s', (raw, target) => {
    expect(parseInspect(raw)).toEqual(target)
    expect(serializeInspect(parseInspect(raw)!)).toBe(raw)
  })

  it.each([
    'nope:1', 'prop:plan:pro', 'time:2026-09-28', 'country:', ':IN', 'country', 'country:IN,DE',
    'city:Springfield', 'city:Springfield,usa', 'region:,US', 'weekday_hour:7,1', 'weekday_hour:1,24',
    'weekday_hour:1', 'weekday_hour:a,1', 'page:%E0%A4%A', 42, undefined, null,
  ])('drops %s', (raw) => {
    expect(parseInspect(raw)).toBeUndefined()
  })

  it('keeps only a valid value when reading route search', () => {
    expect(parseInspectSearch({ period: '7d', inspect: 'country:IN' })).toEqual({ inspect: 'country:IN' })
    expect(parseInspectSearch({ inspect: 'nope:1' })).toEqual({})
    expect(parseInspectSearch(undefined)).toEqual({})
    expect(withoutInspect({ period: '7d', inspect: 'country:IN' })).toEqual({ period: '7d' })
  })

  it('builds targets only for supported values', () => {
    expect(inspectTargetFor('region', ['California', 'US'])).toEqual({ dimension: 'region', values: ['California', 'US'] })
    expect(inspectTargetFor('region', ['California', ''])).toBeUndefined()
    expect(inspectTargetFor('prop:plan', ['pro'])).toBeUndefined()
    expect(inspectTargetFor('country', [''])).toBeUndefined()
  })

  it('toggles the same selection closed and replaces a different one', () => {
    expect(sameInspect(india, { dimension: 'country', values: ['IN'] })).toBe(true)
    expect(sameInspect(india, undefined)).toBe(false)
    expect(nextInspect(undefined, india)).toBe('country:IN')
    expect(nextInspect(india, india)).toBeUndefined()
    expect(nextInspect(india, { dimension: 'country', values: ['DE'] })).toBe('country:DE')
  })

  it('turns a value into the same is filter a row click adds', () => {
    expect(inspectFilter({ dimension: 'city', values: ['Springfield', 'US'] })).toEqual({ dimension: 'city', operator: 'is', values: ['Springfield', 'US'] })
    expect(inspectFilter({ dimension: 'weekday_hour', values: ['1', '13'] })).toBeUndefined()
  })
})

describe('inspect privacy and availability', () => {
  it.each([
    [india, false, true],
    [india, true, true],
    [{ dimension: 'country', values: ['ZZ'] } as InspectTarget, true, false],
    [{ dimension: 'page', values: ['/'] } as InspectTarget, true, false],
    [{ dimension: 'page', values: ['/'] } as InspectTarget, false, true],
    [{ dimension: 'source', values: ['news.example'] } as InspectTarget, true, false],
    [{ dimension: 'channel', values: ['Organic Search'] } as InspectTarget, true, true],
    [{ dimension: 'weekday_hour', values: ['1', '13'] } as InspectTarget, true, true],
  ])('inspectAllowed(%j, anonymous=%s) is %s', (target, anonymous, allowed) => {
    expect(inspectAllowed(target, anonymous)).toBe(allowed)
  })

  it('applies only on the standard dashboard and only when allowed', () => {
    expect(activeInspectTarget('country:IN', { anonymous: false, standardDashboard: true })).toEqual(india)
    expect(activeInspectTarget('country:IN', { anonymous: false, standardDashboard: false })).toBeUndefined()
    expect(activeInspectTarget('page:%2F', { anonymous: true, standardDashboard: true })).toBeUndefined()
    expect(activeInspectTarget('nope:1', { anonymous: false, standardDashboard: true })).toBeUndefined()
  })

  it.each([
    [india, false, ['page', 'source', 'device']],
    [{ dimension: 'city', values: ['Springfield', 'US'] } as InspectTarget, false, ['page', 'source', 'device']],
    [{ dimension: 'entry', values: ['/'] } as InspectTarget, false, ['source', 'country', 'device']],
    [{ dimension: 'page', values: ['/'] } as InspectTarget, false, ['source', 'country', 'device']],
    [india, true, ['channel', 'device']],
    [{ dimension: 'channel', values: ['Email'] } as InspectTarget, true, ['country', 'device']],
    [{ dimension: 'weekday_hour', values: ['1', '13'] } as InspectTarget, false, []],
  ])('cross-breaks %j (anonymous=%s) by %j', (target, anonymous, dimensions) => {
    expect(inspectCrossDimensions(target, anonymous)).toEqual(dimensions)
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && bun run test -- src/lib/inspect.test.ts`
Expected: FAIL with "Failed to resolve import "./inspect"".

- [ ] **Step 3: Implement `lib/inspect.ts`**

Create `web/src/lib/inspect.ts`:

```ts
import type { Filter } from '@/lib/search'
import { isAnonymousDimensionAllowed, isAnonymousFilterAllowed } from '@/lib/tracking-policy'

/** Dimensions the dashboard can inspect: every BreakdownCard tab dimension plus one Weekly rhythm cell. */
export const inspectDimensions = [
  'page', 'entry', 'exit',
  'source', 'channel', 'utm_source', 'utm_medium', 'utm_campaign',
  'country', 'region', 'city', 'language', 'hostname',
  'browser', 'browser_version', 'os', 'os_version', 'device', 'screen_size',
  'goal', 'event',
  'weekday_hour',
] as const

export type InspectDimension = (typeof inspectDimensions)[number]
export type InspectTarget = { dimension: InspectDimension; values: string[] }

/** What a source needs to offer Inspect: the current selection, the privacy check, and the opener. */
export type InspectControl = {
  selected?: InspectTarget
  can(target: InspectTarget): boolean
  open(target: InspectTarget, origin: HTMLElement): void
}

function isInspectDimension(value: string): value is InspectDimension {
  return (inspectDimensions as readonly string[]).includes(value)
}

function validValues(dimension: InspectDimension, values: string[]): boolean {
  if (values.some((value) => value.trim() === '')) return false
  if (dimension === 'weekday_hour') {
    if (values.length !== 2 || !values.every((value) => /^\d{1,2}$/.test(value))) return false
    const [weekday, hour] = values.map(Number)
    return weekday <= 6 && hour <= 23
  }
  // Region and city are scoped to a country, the same [place, ISO country] pair a row filter uses.
  if (dimension === 'region' || dimension === 'city') return values.length === 2 && /^[A-Z]{2}$/.test(values[1])
  return values.length === 1
}

export function inspectTargetFor(dimension: string, values: string[]): InspectTarget | undefined {
  return isInspectDimension(dimension) && validValues(dimension, values) ? { dimension, values: [...values] } : undefined
}

function decode(value: string): string | undefined {
  try {
    return decodeURIComponent(value)
  } catch {
    return undefined
  }
}

/** Reads `<dimension>:<value>[,<value>]`; each value is URI-encoded, as in the `f` filter param. */
export function parseInspect(raw: unknown): InspectTarget | undefined {
  if (typeof raw !== 'string') return undefined
  const separator = raw.indexOf(':')
  if (separator < 1) return undefined
  const values = raw.slice(separator + 1).split(',').map(decode)
  if (values.some((value) => value === undefined)) return undefined
  return inspectTargetFor(raw.slice(0, separator), values as string[])
}

export function serializeInspect(target: InspectTarget): string {
  return `${target.dimension}:${target.values.map((value) => encodeURIComponent(value)).join(',')}`
}

/**
 * The explicit `undefined` matters: TanStack merges the parent match's raw search under a route's validated search,
 * so leaving the key out would let a malformed raw value through to useSearch().
 */
export function parseInspectSearch(raw: unknown): { inspect?: string } {
  const value = raw && typeof raw === 'object' ? (raw as Record<string, unknown>).inspect : undefined
  const target = parseInspect(value)
  return { inspect: target ? serializeInspect(target) : undefined }
}

export function withoutInspect<T extends { inspect?: string }>(search: T): Omit<T, 'inspect'> {
  const copy = { ...search }
  delete copy.inspect
  return copy
}

export function sameInspect(left?: InspectTarget, right?: InspectTarget): boolean {
  return left !== undefined && right !== undefined && left.dimension === right.dimension &&
    left.values.length === right.values.length && left.values.every((value, index) => value === right.values[index])
}

/** Selecting the open value again closes the inspector; anything else replaces it. */
export function nextInspect(current: InspectTarget | undefined, target: InspectTarget): string | undefined {
  return sameInspect(current, target) ? undefined : serializeInspect(target)
}

export function inspectFilter(target: InspectTarget): Filter | undefined {
  if (target.dimension === 'weekday_hour') return undefined
  return { dimension: target.dimension, operator: 'is', values: [...target.values] }
}

/** Anonymous sites inspect only values they could also filter by; a Weekly rhythm cell exposes nothing the grid does not show. */
export function inspectAllowed(target: InspectTarget, anonymous: boolean): boolean {
  if (!anonymous || target.dimension === 'weekday_hour') return true
  const filter = inspectFilter(target)
  return filter !== undefined && isAnonymousDimensionAllowed(target.dimension) && isAnonymousFilterAllowed(filter)
}

export function activeInspectTarget(raw: unknown, context: { anonymous: boolean; standardDashboard: boolean }): InspectTarget | undefined {
  if (!context.standardDashboard) return undefined
  const target = parseInspect(raw)
  return target && inspectAllowed(target, context.anonymous) ? target : undefined
}

export function inspectCrossDimensions(target: InspectTarget, anonymous: boolean): string[] {
  if (target.dimension === 'weekday_hour') return []
  const candidates = anonymous ? ['channel', 'country', 'device'] : ['page', 'source', 'country', 'device']
  return candidates.filter((dimension) =>
    dimension !== target.dimension &&
    !(dimension === 'country' && (target.dimension === 'region' || target.dimension === 'city')) &&
    !(dimension === 'page' && (target.dimension === 'entry' || target.dimension === 'exit')))
}
```

- [ ] **Step 4: Run the unit tests to verify they pass**

Run: `cd web && bun run test -- src/lib/inspect.test.ts`
Expected: PASS.

- [ ] **Step 5: Write the failing route test**

In `web/src/routes/-dashboard-breakdowns.test.tsx`, add this helper directly after `renderDashboard` (it is reused by Tasks 6-8):

```tsx
  function renderAt(...urls: string[]) {
    const history = createMemoryHistory({ initialEntries: urls, initialIndex: urls.length - 1 })
    const router = createRouter({ routeTree, history, parseSearch, stringifySearch })
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><RouterProvider router={router} /></QueryClientProvider>)
    return router
  }
```

Then append:

```tsx
  it('validates the inspect selection in the dashboard search and drops an unknown one', async () => {
    const leafSearch = (router: ReturnType<typeof renderAt>) => router.state.matches.at(-1)?.search as Record<string, unknown> | undefined
    const valid = renderAt('/app/acme/example?period=7d&inspect=country%3AIN')
    await waitFor(() => expect(leafSearch(valid)).toMatchObject({ period: '7d', inspect: 'country:IN' }))
    cleanup()
    const invalid = renderAt('/app/acme/example?period=7d&inspect=nope%3A1')
    await waitFor(() => expect(leafSearch(invalid)).toMatchObject({ period: '7d' }))
    expect(leafSearch(invalid)?.inspect).toBeUndefined()
  })
```

- [ ] **Step 6: Run it to verify it fails**

Run: `cd web && bun run test -- src/routes/-dashboard-breakdowns.test.tsx -t "validates the inspect selection"`
Expected: FAIL. The first `toMatchObject` fails because `inspect` is missing from the validated search.

- [ ] **Step 7: Validate `inspect` in the dashboard route only**

In `web/src/routes/app.$org.$site.index.tsx`, add the import after the `../lib/search` import block:

```tsx
import { parseInspectSearch, withoutInspect } from '../lib/inspect'
```

Replace the route definition's first two options (lines 65-66):

```tsx
  validateSearch: (search) => ({ ...dashboardSearch.parse(search), ...parseInspectSearch(search) }),
  // Opening or closing the inspector must not re-run the dashboard prefetch.
  loaderDeps: ({ search }) => withoutInspect(search),
```

- [ ] **Step 8: Run the tests and typecheck**

Run: `cd web && bun run typecheck && bun run test -- src/lib/inspect.test.ts src/routes/-dashboard-breakdowns.test.tsx`
Expected: typecheck passes (`prefetchDashboard` still receives a `DashboardSearch`-compatible object) and all tests pass.

- [ ] **Step 9: Commit**

```bash
git add web/src/lib/inspect.ts web/src/lib/inspect.test.ts web/src/routes/app.\$org.\$site.index.tsx web/src/routes/-dashboard-breakdowns.test.tsx
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+' || true
git commit -m "feat(web): add a validated inspect search param to the dashboard route"
```

### Task 3: Inspector width storage and the resizable primitive

**Files:**
- Create: `web/src/components/inspectorWidth.ts`, `web/src/components/inspectorWidth.test.ts`, `web/src/components/ui/resizable.tsx`
- Modify: `web/package.json`, `web/bun.lock` (via `bun add`)

**Interfaces:**
- Produces:
  - `INSPECTOR_WIDTH = { min: 20, max: 32, initial: 24 }` (rem)
  - `clampInspectorWidth(rem: number): number`: clamps to 20-32 in 0.25 steps; NaN becomes 24.
  - `readInspectorWidth(): number`: never throws; 24 when storage is missing, blocked or holds garbage.
  - `writeInspectorWidth(rem: number): void`: never throws.
  - `remFromPixels(pixels: number): number`: uses the root font size, 16 if unreadable.
  - `ResizablePanelGroup`, `ResizablePanel` and `ResizableHandle` (with `withHandle?: boolean`) from `@/components/ui/resizable`, typed on the `react-resizable-panels` v4 `GroupProps`, `PanelProps` and `SeparatorProps`.

- [ ] **Step 1: Write the failing test**

Create `web/src/components/inspectorWidth.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from 'vitest'

import { INSPECTOR_WIDTH, clampInspectorWidth, readInspectorWidth, remFromPixels, writeInspectorWidth } from './inspectorWidth'

afterEach(() => {
  vi.restoreAllMocks()
  window.localStorage.clear()
  document.documentElement.style.fontSize = ''
})

describe('inspector width', () => {
  it('starts at 24rem and clamps to 20-32rem in quarter steps', () => {
    expect(readInspectorWidth()).toBe(24)
    expect(clampInspectorWidth(10)).toBe(20)
    expect(clampInspectorWidth(40)).toBe(32)
    expect(clampInspectorWidth(25.13)).toBe(25.25)
    expect(clampInspectorWidth(Number.NaN)).toBe(INSPECTOR_WIDTH.initial)
  })

  it('remembers a width and ignores garbage', () => {
    writeInspectorWidth(28)
    expect(window.localStorage.getItem('supermetry:inspector-width')).toBe('28')
    expect(readInspectorWidth()).toBe(28)
    window.localStorage.setItem('supermetry:inspector-width', 'wide')
    expect(readInspectorWidth()).toBe(24)
    window.localStorage.setItem('supermetry:inspector-width', ' ')
    expect(readInspectorWidth()).toBe(24)
    window.localStorage.setItem('supermetry:inspector-width', '99')
    expect(readInspectorWidth()).toBe(32)
  })

  it('works when the browser blocks storage', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
    expect(readInspectorWidth()).toBe(24)
    expect(() => writeInspectorWidth(30)).not.toThrow()
  })

  it('converts pixels using the root font size', () => {
    document.documentElement.style.fontSize = '20px'
    expect(remFromPixels(480)).toBe(24)
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && bun run test -- src/components/inspectorWidth.test.ts`
Expected: FAIL with "Failed to resolve import "./inspectorWidth"".

- [ ] **Step 3: Implement width storage**

Create `web/src/components/inspectorWidth.ts`:

```ts
/** Inspector widths in rem: spec section 11.1 (default 24, resizable 20-32). */
export const INSPECTOR_WIDTH = { min: 20, max: 32, initial: 24 } as const

const storageKey = 'supermetry:inspector-width'

export function clampInspectorWidth(rem: number): number {
  if (!Number.isFinite(rem)) return INSPECTOR_WIDTH.initial
  return Math.min(INSPECTOR_WIDTH.max, Math.max(INSPECTOR_WIDTH.min, Math.round(rem * 4) / 4))
}

export function readInspectorWidth(): number {
  try {
    const raw = window.localStorage.getItem(storageKey)
    return raw === null || raw.trim() === '' ? INSPECTOR_WIDTH.initial : clampInspectorWidth(Number(raw))
  } catch {
    return INSPECTOR_WIDTH.initial
  }
}

export function writeInspectorWidth(rem: number): void {
  try {
    window.localStorage.setItem(storageKey, String(clampInspectorWidth(rem)))
  } catch {
    // Storage blocked or full: the width simply resets on the next visit.
  }
}

export function remFromPixels(pixels: number): number {
  const root = Number.parseFloat(getComputedStyle(document.documentElement).fontSize)
  return pixels / (Number.isFinite(root) && root > 0 ? root : 16)
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `cd web && bun run test -- src/components/inspectorWidth.test.ts`
Expected: PASS.

- [ ] **Step 5: Add the shadcn resizable primitive**

Run: `cd web && bun add react-resizable-panels@4.11.2`
Expected: `installed react-resizable-panels@4.11.2`; `package.json` and `bun.lock` change.

Create `web/src/components/ui/resizable.tsx` (the shadcn component written against the v4 API: `Group`, `Panel`, `Separator`):

```tsx
import { GripVerticalIcon } from 'lucide-react'
import * as ResizablePrimitive from 'react-resizable-panels'

import { cn } from '@/lib/utils'

function ResizablePanelGroup({ className, ...props }: ResizablePrimitive.GroupProps) {
  return (
    <ResizablePrimitive.Group
      data-slot="resizable-panel-group"
      className={cn('flex h-full w-full aria-[orientation=vertical]:flex-col', className)}
      {...props}
    />
  )
}

function ResizablePanel(props: ResizablePrimitive.PanelProps) {
  return <ResizablePrimitive.Panel data-slot="resizable-panel" {...props} />
}

function ResizableHandle({ withHandle, className, ...props }: ResizablePrimitive.SeparatorProps & { withHandle?: boolean }) {
  return (
    <ResizablePrimitive.Separator
      data-slot="resizable-handle"
      className={cn(
        'relative flex w-px items-center justify-center bg-border ring-offset-background after:absolute after:inset-y-0 after:left-1/2 after:w-1 after:-translate-x-1/2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:outline-hidden',
        className,
      )}
      {...props}
    >
      {withHandle ? (
        <div className="z-10 flex h-4 w-3 items-center justify-center rounded-xs border bg-border">
          <GripVerticalIcon className="size-2.5" />
        </div>
      ) : null}
    </ResizablePrimitive.Separator>
  )
}

export { ResizableHandle, ResizablePanel, ResizablePanelGroup }
```

- [ ] **Step 6: Typecheck and commit**

Run: `cd web && bun run typecheck`
Expected: PASS.

```bash
git add web/src/components/inspectorWidth.ts web/src/components/inspectorWidth.test.ts web/src/components/ui/resizable.tsx web/package.json web/bun.lock
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+' || true
git commit -m "feat(web): add inspector width storage and the shadcn resizable primitive"
```

### Task 4: `DashboardPanels` (layout, sheets, focus and Esc)

**Files:**
- Create: `web/src/hooks/use-min-width.ts`, `web/src/components/DashboardPanels.tsx`, `web/src/components/DashboardPanels.test.tsx`
- Modify: `web/src/components/sticky-offsets.test.ts`, `web/src/messages/ui.ts`, `web/src/messages/locales/de.ui.ts`, `web/src/messages/locales/es.ui.ts`

**Interfaces:**
- Consumes: Task 3's `INSPECTOR_WIDTH`, `readInspectorWidth`, `writeInspectorWidth`, `remFromPixels` and the `ui/resizable` exports; `SIDEBAR_EXPANDED_MIN_WIDTH` (1280) from `./sidebarState`; `useIsMobile` from `@/hooks/use-mobile`.
- Produces:
  - `useMinWidth(minimum: number): boolean` from `@/hooks/use-min-width`.
  - `type InspectorPanel = { key: string; title: string; dimension: string; body: ReactNode; onClose(): void }`
  - `DashboardPanels(props: { main: ReactNode; inspector?: InspectorPanel; returnFocusRef: RefObject<HTMLElement | null> })`
  - `ui.inspector` strings: all of them land here (Tasks 5, 7 and 8 use the rest).

- [ ] **Step 1: Add the `inspector` strings (en, de, es)**

In `web/src/messages/ui.ts`, replace the end of the file's object:

```ts
      users: 'Users',
    } as Record<string, string>,
  },
}
```

with:

```ts
      users: 'Users',
    } as Record<string, string>,
  },
  inspector: {
    inspectValue: (label: string) => `Inspect ${label}`,
    close: 'Close inspector',
    resize: 'Resize inspector',
    loading: 'Loading inspector',
    metrics: 'Metrics for this value',
    topBy: (dimension: string) => `${dimension}: top 5`,
    addFilter: 'Add as filter',
    filterApplied: 'This value is already a filter.',
    tooManyFilters: 'Remove a filter to inspect this value: queries allow at most 10 filters.',
    outOfRange: 'Out of range',
    outOfRangeDetail: 'The current date range and filters exclude this value. Change them or close the inspector.',
    cellHint: 'Select a cell to inspect it.',
    trafficScope: 'Across all matching weekdays and hours in the selected range, in the site time zone.',
    dimensions: {
      page: 'Page', entry: 'Entry page', exit: 'Exit page', source: 'Source', channel: 'Channel',
      utm_source: 'UTM source', utm_medium: 'UTM medium', utm_campaign: 'UTM campaign', country: 'Country',
      region: 'Region', city: 'City', language: 'Language', hostname: 'Hostname', browser: 'Browser',
      browser_version: 'Browser version', os: 'OS', os_version: 'OS version', device: 'Device',
      screen_size: 'Screen size', goal: 'Goal', event: 'Event', weekday_hour: 'Weekly rhythm',
    } as Record<string, string>,
  },
}
```

In `web/src/messages/locales/de.ui.ts`, replace:

```ts
      users: 'Nutzer',
    },
  },
}
```

with:

```ts
      users: 'Nutzer',
    },
  },
  inspector: {
    inspectValue: (label: string) => `${label} untersuchen`,
    close: 'Inspektor schließen',
    resize: 'Inspektorbreite ändern',
    loading: 'Inspektor wird geladen',
    metrics: 'Kennzahlen für diesen Wert',
    topBy: (dimension: string) => `${dimension}: Top 5`,
    addFilter: 'Als Filter hinzufügen',
    filterApplied: 'Dieser Wert ist bereits ein Filter.',
    tooManyFilters: 'Entfernen Sie einen Filter, um diesen Wert zu untersuchen: Abfragen erlauben höchstens 10 Filter.',
    outOfRange: 'Außerhalb des Bereichs',
    outOfRangeDetail: 'Der aktuelle Zeitraum und die Filter schließen diesen Wert aus. Ändern Sie sie oder schließen Sie den Inspektor.',
    cellHint: 'Wählen Sie eine Zelle aus, um sie zu untersuchen.',
    trafficScope: 'Über alle passenden Wochentage und Stunden im gewählten Zeitraum, in der Zeitzone der Website.',
    dimensions: {
      page: 'Seite', entry: 'Einstiegsseite', exit: 'Ausstiegsseite', source: 'Quelle', channel: 'Kanal',
      utm_source: 'UTM-Quelle', utm_medium: 'UTM-Medium', utm_campaign: 'UTM-Kampagne', country: 'Land',
      region: 'Region', city: 'Stadt', language: 'Sprache', hostname: 'Hostname', browser: 'Browser',
      browser_version: 'Browser-Version', os: 'Betriebssystem', os_version: 'Betriebssystem-Version', device: 'Gerät',
      screen_size: 'Bildschirmgröße', goal: 'Ziel', event: 'Ereignis', weekday_hour: 'Wochenrhythmus',
    },
  },
}
```

In `web/src/messages/locales/es.ui.ts`, replace:

```ts
      users: 'Usuarios',
    },
  },
}
```

with:

```ts
      users: 'Usuarios',
    },
  },
  inspector: {
    inspectValue: (label: string) => `Inspeccionar ${label}`,
    close: 'Cerrar inspector',
    resize: 'Cambiar el ancho del inspector',
    loading: 'Cargando inspector',
    metrics: 'Métricas de este valor',
    topBy: (dimension: string) => `${dimension}: top 5`,
    addFilter: 'Añadir como filtro',
    filterApplied: 'Este valor ya es un filtro.',
    tooManyFilters: 'Quita un filtro para inspeccionar este valor: las consultas admiten como máximo 10 filtros.',
    outOfRange: 'Fuera de rango',
    outOfRangeDetail: 'El rango de fechas y los filtros actuales excluyen este valor. Cámbialos o cierra el inspector.',
    cellHint: 'Selecciona una celda para inspeccionarla.',
    trafficScope: 'En todos los días y horas coincidentes del rango seleccionado, en la zona horaria del sitio.',
    dimensions: {
      page: 'Página', entry: 'Página de entrada', exit: 'Página de salida', source: 'Fuente', channel: 'Canal',
      utm_source: 'Fuente UTM', utm_medium: 'Medio UTM', utm_campaign: 'Campaña UTM', country: 'País',
      region: 'Región', city: 'Ciudad', language: 'Idioma', hostname: 'Nombre de host', browser: 'Navegador',
      browser_version: 'Versión del navegador', os: 'Sistema operativo', os_version: 'Versión del sistema operativo', device: 'Dispositivo',
      screen_size: 'Tamaño de pantalla', goal: 'Objetivo', event: 'Evento', weekday_hour: 'Ritmo semanal',
    },
  },
}
```

Run: `cd web && bun run test -- src/messages/locales/locales.test.ts`
Expected: PASS (parity holds).

- [ ] **Step 2: Write the failing panel tests**

Create `web/src/components/DashboardPanels.test.tsx`:

```tsx
import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createRef, useEffect } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { DashboardPanels, type InspectorPanel } from './DashboardPanels'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}

function viewport(width: number) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: width })
}

function panel(overrides: Partial<InspectorPanel> = {}): InspectorPanel {
  return { key: 'country:IN', title: 'India', dimension: 'Country', body: <p>Body</p>, onClose: vi.fn(), ...overrides }
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: vi.fn().mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }),
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  window.localStorage.clear()
})

describe('DashboardPanels', () => {
  it('shows the inspector beside main from 1280px, focuses its heading and offers a labelled resize handle', async () => {
    viewport(1440)
    render(<DashboardPanels main={<p>Main</p>} inspector={panel()} returnFocusRef={createRef()} />)

    const aside = screen.getByRole('complementary', { name: 'India' })
    expect(aside).toHaveClass('sticky', 'top-28')
    expect(screen.getByText('Country')).toBeVisible()
    await waitFor(() => expect(screen.getByRole('heading', { level: 2, name: 'India' })).toHaveFocus())
    expect(screen.getByRole('separator', { name: 'Resize inspector' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.getByText('Main')).toBeVisible()
  })

  it('closes with Esc or the close button beside main, but leaves an Esc another layer handled', () => {
    viewport(1440)
    const onClose = vi.fn()
    const body = <button type="button" onKeyDown={(event) => event.preventDefault()}>Nested</button>
    render(<DashboardPanels main={<p>Main</p>} inspector={panel({ onClose, body })} returnFocusRef={createRef()} />)

    fireEvent.keyDown(screen.getByRole('button', { name: 'Nested' }), { key: 'Escape' })
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.keyDown(screen.getByRole('heading', { name: 'India' }), { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: 'Close inspector' }))
    expect(onClose).toHaveBeenCalledTimes(2)
  })

  it('keeps main mounted and returns focus to the invoking control when the side inspector closes', async () => {
    viewport(1440)
    const mounts = vi.fn()
    function Probe() {
      useEffect(() => mounts(), [])
      return <p>Main</p>
    }
    const origin = document.createElement('button')
    document.body.append(origin)
    const returnFocusRef = { current: origin as HTMLElement | null }
    const { rerender } = render(<DashboardPanels main={<Probe />} returnFocusRef={returnFocusRef} />)
    rerender(<DashboardPanels main={<Probe />} inspector={panel()} returnFocusRef={returnFocusRef} />)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'India' })).toHaveFocus())
    rerender(<DashboardPanels main={<Probe />} returnFocusRef={returnFocusRef} />)

    await waitFor(() => expect(origin).toHaveFocus())
    expect(screen.queryByRole('complementary')).toBeNull()
    expect(mounts).toHaveBeenCalledTimes(1)
    origin.remove()
  })

  it('overlays a right-hand sheet between 768 and 1279px and closes it with Esc', async () => {
    viewport(1024)
    const onClose = vi.fn()
    render(<DashboardPanels main={<p>Main</p>} inspector={panel({ onClose })} returnFocusRef={createRef()} />)

    const sheet = await screen.findByRole('dialog', { name: 'India' })
    expect(sheet).toHaveAttribute('data-side', 'right')
    expect(sheet).toHaveAccessibleDescription('Country')
    expect(screen.queryByRole('separator')).toBeNull()
    expect(screen.getAllByRole('button', { name: /close/i })).toHaveLength(1)
    fireEvent.keyDown(sheet, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })

  it('uses a full-width bottom sheet at 85% height below 768px', async () => {
    viewport(390)
    render(<DashboardPanels main={<p>Main</p>} inspector={panel()} returnFocusRef={createRef()} />)

    const sheet = await screen.findByRole('dialog', { name: 'India' })
    expect(sheet).toHaveAttribute('data-side', 'bottom')
    expect(sheet).toHaveClass('data-[side=bottom]:h-[85dvh]')
  })

  it('returns focus to the invoking control when the sheet closes', async () => {
    viewport(1024)
    const origin = document.createElement('button')
    document.body.append(origin)
    const returnFocusRef = { current: origin as HTMLElement | null }
    const { rerender } = render(<DashboardPanels main={<p>Main</p>} inspector={panel()} returnFocusRef={returnFocusRef} />)
    await screen.findByRole('dialog', { name: 'India' })
    rerender(<DashboardPanels main={<p>Main</p>} returnFocusRef={returnFocusRef} />)

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(origin).toHaveFocus())
    origin.remove()
  })
})
```

Extend `EXPECTED` in `web/src/components/sticky-offsets.test.ts` with:

```ts
  'src/components/DashboardPanels.tsx: sticky top-28',
```

- [ ] **Step 3: Run them to verify they fail**

Run: `cd web && bun run test -- src/components/DashboardPanels.test.tsx src/components/sticky-offsets.test.ts`
Expected: FAIL with "Failed to resolve import "./DashboardPanels"", and the sticky test misses the new entry.

- [ ] **Step 4: Add the width hook**

Create `web/src/hooks/use-min-width.ts`:

```ts
import * as React from 'react'

/** True while the viewport is at least `minimum` px wide; same matchMedia pattern as useIsMobile. */
export function useMinWidth(minimum: number): boolean {
  const [matches, setMatches] = React.useState(() => typeof window !== 'undefined' && window.innerWidth >= minimum)

  React.useEffect(() => {
    const query = window.matchMedia(`(min-width: ${minimum}px)`)
    const onChange = () => setMatches(window.innerWidth >= minimum)
    query.addEventListener('change', onChange)
    onChange()
    return () => query.removeEventListener('change', onChange)
  }, [minimum])

  return matches
}
```

- [ ] **Step 5: Implement `DashboardPanels`**

Create `web/src/components/DashboardPanels.tsx`:

```tsx
import { XIcon } from 'lucide-react'
import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from 'react'

import { Button } from '@/components/ui/button'
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from '@/components/ui/resizable'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { useIsMobile } from '@/hooks/use-mobile'
import { useMinWidth } from '@/hooks/use-min-width'
import { ui } from '@/messages/ui'

import { INSPECTOR_WIDTH, readInspectorWidth, remFromPixels, writeInspectorWidth } from './inspectorWidth'
import { SIDEBAR_EXPANDED_MIN_WIDTH } from './sidebarState'

export type InspectorPanel = {
  /** Identifies the inspected value; focus moves to the heading whenever it changes. */
  key: string
  title: string
  dimension: string
  body: ReactNode
  onClose(): void
}

type DashboardPanelsProps = {
  main: ReactNode
  inspector?: InspectorPanel
  /** The control that opened the inspector; focus returns to it on close. */
  returnFocusRef: RefObject<HTMLElement | null>
}

export function DashboardPanels({ main, inspector, returnFocusRef }: DashboardPanelsProps) {
  const mobile = useIsMobile()
  const wide = useMinWidth(SIDEBAR_EXPANDED_MIN_WIDTH) && !mobile
  const headingId = useId()
  const headingRef = useRef<HTMLHeadingElement>(null)
  const [width] = useState(readInspectorWidth)
  const wasOpen = useRef(false)
  // The sheet animates out after the selection is cleared; keep its last content for that frame.
  const lastInspector = useRef(inspector)
  if (inspector) lastInspector.current = inspector
  const key = inspector?.key

  useEffect(() => {
    if (!wide) return
    if (key !== undefined) {
      wasOpen.current = true
      headingRef.current?.focus()
      return
    }
    if (!wasOpen.current) return
    wasOpen.current = false
    const origin = returnFocusRef.current
    if (origin?.isConnected) origin.focus()
  }, [key, wide, returnFocusRef])

  const closeOnEscape = (event: KeyboardEvent<HTMLElement>) => {
    // A dialog opened from the inspector (full report) handles its own Esc first and marks it handled.
    if (event.key !== 'Escape' || event.defaultPrevented) return
    event.preventDefault()
    inspector?.onClose()
  }

  if (wide) {
    return (
      <ResizablePanelGroup className="items-stretch" orientation="horizontal" style={{ height: 'auto', overflow: 'visible' }}>
        <ResizablePanel id="dashboard-main" minSize="40rem" style={{ overflow: 'visible', maxHeight: 'none' }}>
          <div className="flex min-w-0 flex-col gap-4">{main}</div>
        </ResizablePanel>
        {inspector ? (
          <>
            <ResizableHandle withHandle aria-label={ui.inspector.resize} className="mx-2" />
            <ResizablePanel
              id="dashboard-inspector"
              defaultSize={`${width}rem`}
              groupResizeBehavior="preserve-pixel-size"
              maxSize={`${INSPECTOR_WIDTH.max}rem`}
              minSize={`${INSPECTOR_WIDTH.min}rem`}
              style={{ overflow: 'visible', maxHeight: 'none' }}
              onResize={(size) => {
                if (size.inPixels > 0) writeInspectorWidth(remFromPixels(size.inPixels))
              }}
            >
              <aside
                aria-labelledby={headingId}
                className="sticky top-28 flex max-h-[calc(100dvh-7.5rem)] min-w-0 flex-col overflow-y-auto rounded-xl border bg-card text-card-foreground"
                onKeyDown={closeOnEscape}
              >
                <header className="flex items-start justify-between gap-2 border-b p-4">
                  <div className="flex min-w-0 flex-col gap-0.5">
                    <p className="text-xs text-muted-foreground">{inspector.dimension}</p>
                    <h2 ref={headingRef} className="break-words text-sm font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring" id={headingId} tabIndex={-1}>
                      {inspector.title}
                    </h2>
                  </div>
                  <Button aria-label={ui.inspector.close} size="icon-sm" type="button" variant="ghost" onClick={inspector.onClose}>
                    <XIcon />
                  </Button>
                </header>
                {inspector.body}
              </aside>
            </ResizablePanel>
          </>
        ) : null}
      </ResizablePanelGroup>
    )
  }

  const shown = inspector ?? lastInspector.current
  return (
    <>
      <div className="flex min-w-0 flex-col gap-4">{main}</div>
      <Sheet open={inspector !== undefined} onOpenChange={(open) => { if (!open) inspector?.onClose() }}>
        <SheetContent
          className={mobile
            ? 'gap-0 overflow-y-auto p-0 data-[side=bottom]:h-[85dvh]'
            : 'gap-0 overflow-y-auto p-0 data-[side=right]:w-[min(24rem,100vw)]'}
          showCloseButton={false}
          side={mobile ? 'bottom' : 'right'}
          onCloseAutoFocus={(event) => {
            const origin = returnFocusRef.current
            if (!origin?.isConnected) return
            event.preventDefault()
            origin.focus()
          }}
        >
          {shown ? (
            <>
              <SheetHeader className="flex-row items-start justify-between gap-2 border-b">
                <div className="flex min-w-0 flex-col gap-0.5">
                  <SheetDescription className="text-xs">{shown.dimension}</SheetDescription>
                  <SheetTitle className="break-words">{shown.title}</SheetTitle>
                </div>
                <Button aria-label={ui.inspector.close} size="icon-sm" type="button" variant="ghost" onClick={shown.onClose}>
                  <XIcon />
                </Button>
              </SheetHeader>
              {shown.body}
            </>
          ) : null}
        </SheetContent>
      </Sheet>
    </>
  )
}
```

Why `top-28`: the 44 px header (`top-0`, `h-11`) plus the Task 1 filter row (`py-2` around the 44 px `min-h-11` DatePicker button) end at 6.5rem, so 7rem clears both.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web && bun run test -- src/components/DashboardPanels.test.tsx src/components/sticky-offsets.test.ts`
Expected: PASS. If the side-by-side tests fail inside `react-resizable-panels` (not in your code), debug with superpowers:systematic-debugging before touching the assertions. The plan-time probe rendered `Group`, `Panel` and `Separator` under the same `ResizeObserver` stub.

- [ ] **Step 7: Typecheck and commit**

Run: `cd web && bun run typecheck`
Expected: PASS.

```bash
git add web/src/hooks/use-min-width.ts web/src/components/DashboardPanels.tsx web/src/components/DashboardPanels.test.tsx web/src/components/sticky-offsets.test.ts web/src/messages/ui.ts web/src/messages/locales/de.ui.ts web/src/messages/locales/es.ui.ts
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+' || true
git commit -m "feat(web): add DashboardPanels with a resizable side inspector and sheet fallbacks"
```

### Task 5: The `Inspector` body

**Files:**
- Create: `web/src/lib/breakdown-labels.ts`, `web/src/components/Inspector.tsx`, `web/src/components/Inspector.test.tsx`
- Modify: `web/src/components/BreakdownCard.tsx:39-52` (move `screenSizeLabel` and `breakdownLabel` out), `web/src/components/StatsStrip.tsx:8,78` (export), `web/src/features/map/trafficGrid.ts` and `web/src/features/map/TrafficGrid.tsx:38-45` (move `hourLabel`)

**Interfaces:**
- Consumes: Task 2's `InspectTarget`, `inspectFilter` and `inspectCrossDimensions`; `useStats`, `useBreakdown` and `StatsQuery` from `@/lib/stats`; `FullReportDialog` (lazy).
- Produces:
  - `breakdownLabel(dimension: string, value: string): string` from `@/lib/breakdown-labels`.
  - `hourLabel(hour: number, locale?: string): string` from `@/features/map/trafficGrid`.
  - `statMetrics` and `metricValue(metric: StatMetric, value: number): string` exported from `@/components/StatsStrip`.
  - `type InspectorProps = { target: InspectTarget; query: StatsQuery; anonymous: boolean; trafficMetric: TrafficMetric; onFilter(filter: Filter): void }`
  - `Inspector(props: InspectorProps)`, `inspectorTitle(target: InspectTarget): string` and `inspectorDimensionLabel(dimension: string): string` from `@/components/Inspector`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/Inspector.test.tsx`:

```tsx
import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { InspectTarget } from '@/lib/inspect'
import type { StatsQuery } from '@/lib/stats'

const { useStats, useBreakdown, refetch } = vi.hoisted(() => ({ useStats: vi.fn(), useBreakdown: vi.fn(), refetch: vi.fn() }))
vi.mock('@/lib/stats', () => ({ useStats, useBreakdown }))

import { Inspector, inspectorTitle } from './Inspector'

const query: StatsQuery = {
  siteId: 7,
  dateRange: { from: '2026-09-28', to: '2026-09-30' },
  metrics: ['visitors'],
  filters: [],
  timezone: 'UTC',
  compare: 'previous_period',
}
const india: InspectTarget = { dimension: 'country', values: ['IN'] }
const statMetrics = ['visitors', 'visits', 'pageviews', 'views_per_visit', 'bounce_rate', 'visit_duration', 'engagement_time']

function ok(data: unknown) {
  return { data, error: null, isError: false, isPending: false, refetch }
}

function totals(visitors: number) {
  return ok({ columns: statMetrics, rows: [], totals: { visitors, visits: 3, pageviews: 9, views_per_visit: 3, bounce_rate: 50, visit_duration: 60, engagement_time: 30 }, meta: { sources_used: ['daily'], cached: false } })
}

function rows(dimension: string, value: string, visitors: number) {
  return ok({ columns: [dimension, 'visitors'], rows: [{ [dimension]: value, visitors }], totals: {}, meta: { sources_used: ['raw'], cached: false } })
}

beforeEach(() => {
  vi.clearAllMocks()
  useStats.mockReturnValue(totals(42))
  useBreakdown.mockImplementation((request: StatsQuery) => rows(request.dimensions?.[0] ?? 'page', 'Example', 7))
})

afterEach(cleanup)

describe('Inspector', () => {
  it('shows the value metrics from the stats-strip query with the value added as a filter', () => {
    render(<Inspector anonymous={false} query={query} target={india} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(useStats).toHaveBeenCalledWith(expect.objectContaining({
      metrics: statMetrics,
      dimensions: undefined,
      compare: undefined,
      filters: [{ dimension: 'country', operator: 'is', values: ['IN'] }],
    }))
    const metrics = screen.getByRole('region', { name: 'Metrics for this value' })
    expect(metrics).toHaveTextContent('Visitors')
    expect(metrics).toHaveTextContent('42')
    expect(metrics).toHaveTextContent('Bounce Rate')
  })

  it('cross-breaks the value by the other supported dimensions, five rows each', () => {
    render(<Inspector anonymous={false} query={query} target={india} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(useBreakdown.mock.calls.map(([request]) => request.dimensions)).toEqual([['page'], ['source'], ['device']])
    for (const [request] of useBreakdown.mock.calls) {
      expect(request).toMatchObject({ metrics: ['visitors'], limit: 5, compare: undefined, filters: [{ dimension: 'country', operator: 'is', values: ['IN'] }] })
    }
    expect(screen.getByRole('region', { name: 'Page: top 5' })).toHaveTextContent('Example7')
  })

  it('limits anonymous cross-breakdowns to safe dimensions', () => {
    render(<Inspector anonymous query={query} target={india} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(useBreakdown.mock.calls.map(([request]) => request.dimensions)).toEqual([['channel'], ['device']])
  })

  it('scopes a city to its country and skips the country breakdown', () => {
    render(<Inspector anonymous={false} query={query} target={{ dimension: 'city', values: ['Springfield', 'US'] }} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(useStats).toHaveBeenCalledWith(expect.objectContaining({ filters: [{ dimension: 'city', operator: 'is', values: ['Springfield', 'US'] }] }))
    expect(useBreakdown.mock.calls.map(([request]) => request.dimensions)).toEqual([['page'], ['source'], ['device']])
  })

  it('says Out of range instead of closing when the filters exclude the value', () => {
    useStats.mockReturnValue(totals(0))
    render(<Inspector anonymous={false} query={query} target={india} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(screen.getByRole('status')).toHaveTextContent('Out of range')
    expect(screen.queryByRole('region', { name: 'Metrics for this value' })).toBeNull()
  })

  it('adds the value as a filter, and says so once it is one', () => {
    const onFilter = vi.fn()
    const { rerender } = render(<Inspector anonymous={false} query={query} target={india} trafficMetric="visitors" onFilter={onFilter} />)
    fireEvent.click(screen.getByRole('button', { name: 'Add as filter' }))
    expect(onFilter).toHaveBeenCalledWith({ dimension: 'country', operator: 'is', values: ['IN'] })

    useStats.mockClear()
    const filtered = { ...query, filters: [{ dimension: 'country', operator: 'is' as const, values: ['IN'] }] }
    rerender(<Inspector anonymous={false} query={filtered} target={india} trafficMetric="visitors" onFilter={onFilter} />)
    expect(screen.queryByRole('button', { name: 'Add as filter' })).toBeNull()
    expect(screen.getByText('This value is already a filter.')).toBeVisible()
    expect(useStats).toHaveBeenLastCalledWith(expect.objectContaining({ filters: filtered.filters }))
  })

  it('sends nothing and explains when ten filters are already applied', () => {
    const full = { ...query, filters: Array.from({ length: 10 }, (_, index) => ({ dimension: 'page', operator: 'is_not' as const, values: [`/${index}`] })) }
    render(<Inspector anonymous={false} query={full} target={india} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(screen.getByRole('status')).toHaveTextContent('queries allow at most 10 filters')
    expect(useStats).not.toHaveBeenCalled()
    expect(useBreakdown).not.toHaveBeenCalled()
  })

  it('shows a retryable error when the metrics fail', () => {
    useStats.mockReturnValue({ data: undefined, error: { detail: 'Upstream failed' }, isError: true, isPending: false, refetch })
    render(<Inspector anonymous={false} query={query} target={india} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(screen.getByRole('alert')).toHaveTextContent('Upstream failed')
  })

  it('reads a Weekly rhythm cell from the cached grid query and offers no filter', () => {
    useBreakdown.mockReturnValue(ok({ columns: ['weekday', 'hour_of_day', 'pageviews'], rows: [{ weekday: 1, hour_of_day: 13, pageviews: 178 }], totals: {}, meta: { sources_used: ['raw'], cached: false } }))
    render(<Inspector anonymous={false} query={query} target={{ dimension: 'weekday_hour', values: ['1', '13'] }} trafficMetric="pageviews" onFilter={vi.fn()} />)

    expect(useBreakdown).toHaveBeenCalledWith({ ...query, metrics: ['pageviews'], dimensions: ['weekday', 'hour_of_day'], limit: 168 })
    expect(useStats).not.toHaveBeenCalled()
    expect(screen.getByText('178')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Add as filter' })).toBeNull()
  })

  it('says Out of range for a Weekly rhythm cell with no traffic', () => {
    useBreakdown.mockReturnValue(ok({ columns: [], rows: [], totals: {}, meta: { sources_used: ['raw'], cached: false } }))
    render(<Inspector anonymous={false} query={query} target={{ dimension: 'weekday_hour', values: ['0', '3'] }} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(screen.getByRole('status')).toHaveTextContent('Out of range')
  })

  it('titles a value the way the cards label it', () => {
    expect(inspectorTitle(india)).toBe('India')
    expect(inspectorTitle({ dimension: 'city', values: ['Springfield', 'US'] })).toBe('Springfield, United States')
    expect(inspectorTitle({ dimension: 'screen_size', values: ['mobile'] })).toBe('Mobile (<768px)')
    expect(inspectorTitle({ dimension: 'weekday_hour', values: ['1', '13'] })).toBe('Monday 1 PM')
  })
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && bun run test -- src/components/Inspector.test.tsx`
Expected: FAIL with "Failed to resolve import "./Inspector"".

- [ ] **Step 3: Move the shared helpers**

Create `web/src/lib/breakdown-labels.ts` with the two functions moved verbatim from `BreakdownCard.tsx:39-52`:

```ts
import { countryLabel } from '@/lib/country'
import { ui } from '@/messages/ui'

function screenSizeLabel(value: string): string | undefined {
  const labels: Record<string, string> = {
    mobile: ui.breakdown.mobile, tablet: ui.breakdown.tablet, laptop: ui.breakdown.laptop,
    desktop: ui.breakdown.desktop, large: ui.breakdown.large,
  }
  return labels[value]
}

/** The label a breakdown row shows for a raw dimension value. */
export function breakdownLabel(dimension: string, value: string): string {
  if (value === '') return ui.breakdown.unknown
  if (dimension === 'country') return countryLabel(value)
  if (dimension === 'screen_size') return screenSizeLabel(value) ?? value
  return value
}
```

In `web/src/components/BreakdownCard.tsx`, delete `screenSizeLabel` and `breakdownLabel` (lines 39-52) and add `import { breakdownLabel } from '@/lib/breakdown-labels'` after the `@/lib/breakdown-metrics` import. `countryLabel` stays imported because `filterAccessibleLabel` uses it.

In `web/src/features/map/trafficGrid.ts`, append the function moved verbatim from `TrafficGrid.tsx:38-45`:

```ts
export function hourLabel(hour: number, locale?: string): string {
  const probe = new Intl.DateTimeFormat(locale, { hour: 'numeric', timeZone: 'UTC' })
  const options = probe.resolvedOptions()
  return new Intl.DateTimeFormat(locale, options.hour12
    ? { hour: 'numeric', timeZone: 'UTC' }
    : { hour: '2-digit', minute: '2-digit', timeZone: 'UTC' }
  ).format(new Date(Date.UTC(2020, 0, 1, hour)))
}
```

In `web/src/features/map/TrafficGrid.tsx`, delete the local `hourLabel` (lines 38-45) and add `hourLabel,` as the first name in the existing `import { marginTotals, ... } from './trafficGrid'` list.

In `web/src/components/StatsStrip.tsx`, change `const statMetrics = [` (line 8) to `export const statMetrics = [` and `function metricValue(` (line 78) to `export function metricValue(`.

Run: `cd web && bun run test -- src/components/breakdown-card.test.tsx src/features/map/TrafficGrid.test.tsx` and expect PASS (pure moves).

- [ ] **Step 4: Implement `Inspector`**

Create `web/src/components/Inspector.tsx`:

```tsx
import { lazy, Suspense, useRef, useState } from 'react'

import { ErrorState } from '@/components/ErrorState'
import { metricValue, statMetrics } from '@/components/StatsStrip'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { hourLabel, orderedWeekdays, type TrafficMetric } from '@/features/map/trafficGrid'
import { breakdownLabel } from '@/lib/breakdown-labels'
import { metricLabel } from '@/lib/breakdown-metrics'
import { countryLabel } from '@/lib/country'
import { fmt } from '@/lib/format'
import { inspectCrossDimensions, inspectFilter, type InspectTarget } from '@/lib/inspect'
import type { Filter } from '@/lib/search'
import { useBreakdown, useStats, type StatsQuery } from '@/lib/stats'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'

const FullReportDialog = lazy(() => import('./FullReportDialog').then((module) => ({ default: module.FullReportDialog })))

/** QueryRequest.filters allows at most 10 entries (internal/api/openapi.yaml). */
const MAX_FILTERS = 10

export type InspectorProps = {
  target: InspectTarget
  query: StatsQuery
  anonymous: boolean
  trafficMetric: TrafficMetric
  onFilter(filter: Filter): void
}

export function inspectorDimensionLabel(dimension: string): string {
  return ui.inspector.dimensions[dimension] ?? dimension
}

export function inspectorTitle(target: InspectTarget): string {
  if (target.dimension === 'weekday_hour') {
    const weekday = orderedWeekdays().find((day) => day.index === Number(target.values[0]))?.name ?? target.values[0]
    return `${weekday} ${hourLabel(Number(target.values[1]))}`
  }
  const [value, country] = target.values
  const label = breakdownLabel(target.dimension, value)
  return country === undefined ? label : `${label}, ${countryLabel(country)}`
}

function sameFilter(left: StatsQuery['filters'][number], right: Filter): boolean {
  return left.dimension === right.dimension && left.operator === right.operator &&
    left.values.length === right.values.length && left.values.every((value, index) => value === right.values[index])
}

export function Inspector(props: InspectorProps) {
  if (props.target.dimension === 'weekday_hour') return <TrafficCellBody {...props} />
  const filter = inspectFilter(props.target)
  if (!filter) return null
  const applied = props.query.filters.some((current) => sameFilter(current, filter))
  if (!applied && props.query.filters.length >= MAX_FILTERS) {
    return <p className="p-4 text-sm text-muted-foreground" role="status">{ui.inspector.tooManyFilters}</p>
  }
  return <ValueBody {...props} applied={applied} filter={filter} />
}

function InspectorLoading() {
  return (
    <div aria-label={ui.inspector.loading} className="flex flex-col gap-3 p-4" role="status">
      {Array.from({ length: 4 }, (_, index) => <Skeleton key={index} className="h-8 w-full" />)}
    </div>
  )
}

function OutOfRange() {
  return (
    <div className="flex flex-col gap-1 p-4" role="status">
      <p className="text-sm font-medium">{ui.inspector.outOfRange}</p>
      <p className="text-sm text-muted-foreground">{ui.inspector.outOfRangeDetail}</p>
    </div>
  )
}

function ValueBody({ target, query, anonymous, onFilter, applied, filter }: InspectorProps & { applied: boolean; filter: Filter }) {
  const reportRef = useRef<HTMLButtonElement>(null)
  const [reportOpen, setReportOpen] = useState(false)
  const scoped: StatsQuery = { ...query, filters: applied ? query.filters : [...query.filters, filter] }
  // The request StatsStrip sends once this value is a filter, so the numbers match the dashboard after "Add as filter".
  const totals = useStats({ ...scoped, metrics: [...statMetrics], dimensions: undefined, compare: undefined })
  const reportMetric = target.dimension === 'goal' ? 'conversions' : target.dimension === 'event' ? 'events' : 'visitors'
  const reportQuery: StatsQuery = {
    ...query,
    metrics: [reportMetric],
    dimensions: target.dimension === 'region' || target.dimension === 'city' ? [target.dimension, 'country'] : [target.dimension],
    orderBy: [{ column: reportMetric, direction: 'desc' }],
    limit: 10,
  }

  if (totals.isError) return <ErrorState detail={totals.error?.detail} onRetry={() => void totals.refetch()} />
  if (totals.isPending) return <InspectorLoading />
  const values = totals.data?.totals ?? {}
  if (!(Number(values.visitors ?? 0) > 0)) return <OutOfRange />

  return (
    <>
      <section aria-label={ui.inspector.metrics} className="grid grid-cols-2 gap-2 p-4">
        {statMetrics.map((metric) => (
          <div key={metric} className="rounded-lg bg-muted/50 p-3">
            <p className="text-xs text-muted-foreground">{metricLabel(metric)}</p>
            <p className="mt-1 font-medium tabular-nums">{metricValue(metric, Number(values[metric] ?? 0))}</p>
          </div>
        ))}
      </section>
      <div className="flex flex-col gap-4 border-t p-4 empty:hidden">
        {inspectCrossDimensions(target, anonymous).map((dimension) => <CrossBreakdown key={dimension} dimension={dimension} query={scoped} />)}
      </div>
      <div className="flex flex-wrap items-center gap-2 border-t p-4">
        {applied
          ? <p className="text-sm text-muted-foreground">{ui.inspector.filterApplied}</p>
          : <Button size="sm" type="button" onClick={() => onFilter(filter)}>{ui.inspector.addFilter}</Button>}
        <Button ref={reportRef} aria-haspopup="dialog" size="sm" type="button" variant="outline" onClick={() => setReportOpen(true)}>
          {en.dashboard.viewFullReport}
        </Button>
      </div>
      {reportOpen ? (
        <Suspense fallback={null}>
          <FullReportDialog
            allowSearch={!anonymous}
            dimension={target.dimension}
            query={reportQuery}
            returnFocusRef={reportRef}
            title={inspectorDimensionLabel(target.dimension)}
            onClose={() => setReportOpen(false)}
          />
        </Suspense>
      ) : null}
    </>
  )
}

function CrossBreakdown({ dimension, query }: { dimension: string; query: StatsQuery }) {
  const result = useBreakdown({ ...query, metrics: ['visitors'], dimensions: [dimension], limit: 5, compare: undefined })
  const title = ui.inspector.topBy(inspectorDimensionLabel(dimension))
  if (result.isError) return <ErrorState detail={result.error?.detail} onRetry={() => void result.refetch()} />
  if (result.isPending) return <Skeleton aria-label={title} className="h-24 w-full" />
  const rows = result.data?.rows ?? []
  if (rows.length === 0) return null
  return (
    <section aria-label={title}>
      <h3 className="text-sm font-medium">{title}</h3>
      <ol className="mt-2 flex flex-col gap-1">
        {rows.map((row, index) => (
          <li key={`${String(row[dimension] ?? '')}:${index}`} className="flex min-w-0 items-center justify-between gap-2 text-sm">
            <span className="truncate text-muted-foreground">{breakdownLabel(dimension, String(row[dimension] ?? ''))}</span>
            <span className="tabular-nums">{fmt.number(Number(row.visitors ?? 0))}</span>
          </li>
        ))}
      </ol>
    </section>
  )
}

function TrafficCellBody({ target, query, trafficMetric }: InspectorProps) {
  // Identical to WeeklyRhythmCard's request, so it reads the cached grid instead of fetching again.
  const traffic = useBreakdown({ ...query, metrics: [trafficMetric], dimensions: ['weekday', 'hour_of_day'], limit: 168 })
  if (traffic.isError) return <ErrorState detail={traffic.error?.detail} onRetry={() => void traffic.refetch()} />
  if (traffic.isPending) return <InspectorLoading />
  const row = (traffic.data?.rows ?? []).find((candidate) =>
    Number(candidate.weekday) === Number(target.values[0]) && Number(candidate.hour_of_day) === Number(target.values[1]))
  const value = Number(row?.[trafficMetric] ?? 0)
  if (!row || !(value > 0)) return <OutOfRange />
  return (
    <div className="flex flex-col gap-3 p-4">
      <div className="rounded-lg bg-muted/50 p-3">
        <p className="text-xs text-muted-foreground">{metricLabel(trafficMetric)}</p>
        <p className="mt-1 font-medium tabular-nums">{fmt.number(value)}</p>
      </div>
      <p className="text-sm text-muted-foreground">{ui.inspector.trafficScope}</p>
    </div>
  )
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd web && bun run test -- src/components/Inspector.test.tsx src/components/breakdown-card.test.tsx src/features/map/TrafficGrid.test.tsx`
Expected: PASS. If `fmt.number(42)` or `fmt.number(178)` renders differently from `42` or `178`, check `web/src/lib/format.ts` and adjust only the expected text, never the component.

- [ ] **Step 6: Typecheck and commit**

Run: `cd web && bun run typecheck`
Expected: PASS.

```bash
git add web/src/lib/breakdown-labels.ts web/src/components/Inspector.tsx web/src/components/Inspector.test.tsx web/src/components/BreakdownCard.tsx web/src/components/StatsStrip.tsx web/src/features/map/trafficGrid.ts web/src/features/map/TrafficGrid.tsx
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+' || true
git commit -m "feat(web): add the inspector body built from existing stats and breakdown queries"
```

### Task 6: Wire the inspector into the dashboard route, then check the budget

**Files:**
- Modify: `web/src/routes/app.$org.$site.index.tsx` (imports, `useRef`, the standard branch at lines 262-291)
- Test: `web/src/routes/-dashboard-breakdowns.test.tsx`

**Interfaces:**
- Consumes: `activeInspectTarget` and `serializeInspect` (Task 2); `DashboardPanels` (Task 4); `Inspector`, `inspectorTitle` and `inspectorDimensionLabel` (Task 5).
- Produces, inside `DashboardStateRoute`: `inspectorOrigin: RefObject<HTMLElement | null>`, `standardDashboard: boolean`, `inspectTarget: InspectTarget | undefined`, `closeInspector(): void` and `trafficMetric: TrafficMetric`. Task 7 adds the opener next to them.

- [ ] **Step 1: Write the failing route tests**

Append to `web/src/routes/-dashboard-breakdowns.test.tsx` (`renderAt` comes from Task 2):

```tsx
  const range = '/app/acme/example?period=custom&from=2026-09-28&to=2026-09-30'
  const anonymousMe = { ...me, memberships: [{ ...me.memberships[0], sites: [{ ...me.memberships[0].sites[0], tracking_policy: { instance_mode: 'anonymous', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }] }] }

  it('restores the inspector from the URL without touching filters and closes it with Esc', async () => {
    const router = renderAt(`${range}&inspect=country%3AIN`)
    const inspector = await screen.findByRole('dialog', { name: 'India' })
    expect(inspector).toHaveAccessibleDescription('Country')
    expect(await within(inspector).findByRole('region', { name: 'Metrics for this value' })).toBeVisible()
    expect(post).toHaveBeenCalledWith('/api/private/query', expect.objectContaining({
      body: expect.objectContaining({
        metrics: ['visitors', 'visits', 'pageviews', 'views_per_visit', 'bounce_rate', 'visit_duration', 'engagement_time'],
        filters: [{ dimension: 'country', operator: 'is', values: ['IN'] }],
      }),
    }))
    expect(router.state.location.search.f ?? []).toEqual([])

    fireEvent.keyDown(inspector, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'India' })).toBeNull())
    expect(router.state.location.search.inspect).toBeUndefined()
    expect(router.state.location.href).not.toContain('inspect')
    expect(router.state.location.search.period).toBe('custom')
  })

  it('closes the inspector on Back', async () => {
    const router = renderAt(range, `${range}&inspect=country%3AIN`)
    await screen.findByRole('dialog', { name: 'India' })
    router.history.back()
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'India' })).toBeNull())
  })

  it('adds the inspected value as a filter and keeps the inspector open', async () => {
    const router = renderAt(`${range}&inspect=country%3AIN`)
    const inspector = await screen.findByRole('dialog', { name: 'India' })
    fireEvent.click(await within(inspector).findByRole('button', { name: 'Add as filter' }))
    await waitFor(() => expect(router.state.location.search.f).toEqual(['country:is:IN']))
    expect(router.state.location.search.inspect).toBe('country:IN')
    expect(await within(screen.getByRole('dialog', { name: 'India' })).findByText('This value is already a filter.')).toBeVisible()
  })

  it('opens only privacy-safe selections on an anonymous site', async () => {
    get.mockResolvedValue({ data: anonymousMe, response: new Response(null, { status: 200 }) })
    renderAt(`${range}&inspect=page%3A%252F`)
    await screen.findByRole('region', { name: 'Verified pages' })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(post.mock.calls.some(([, options]) => (options.body.filters ?? []).some((filter: { dimension: string }) => filter.dimension === 'page'))).toBe(false)
    cleanup()

    get.mockClear()
    renderAt(`${range}&inspect=country%3AIN`)
    expect(await screen.findByRole('dialog', { name: 'India' })).toBeVisible()
    const scoped = () => get.mock.calls.filter(([path, options]) => path === '/api/private/stats/breakdown' && ((options?.params?.query?.filter ?? []) as string[]).includes('country:is:IN'))
    await waitFor(() => expect(scoped().length).toBeGreaterThanOrEqual(2))
    expect(new Set(scoped().flatMap(([, options]) => options.params.query.dimension as string[]))).toEqual(new Set(['channel', 'device']))
  })
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && bun run test -- src/routes/-dashboard-breakdowns.test.tsx`
Expected: FAIL in the four new tests ("Unable to find role="dialog" and name "India""). The anonymous test's first half passes; its second half fails.

- [ ] **Step 3: Wire the route**

In `web/src/routes/app.$org.$site.index.tsx`:

1. Change the React import (line 3) to `import { lazy, Suspense, useEffect, useRef, useState } from 'react'`.
2. Replace the Task 2 import `import { parseInspectSearch, withoutInspect } from '../lib/inspect'` with:

```tsx
import { activeInspectTarget, parseInspectSearch, serializeInspect, withoutInspect } from '../lib/inspect'
import { DashboardPanels } from '@/components/DashboardPanels'
import { Inspector, inspectorDimensionLabel, inspectorTitle } from '@/components/Inspector'
```

3. Directly after `const [edit, setEdit] = useState<EditState>(null)` add (hooks must stay above the early returns):

```tsx
  // The control that opened the inspector; focus returns to it on close.
  const inspectorOrigin = useRef<HTMLElement | null>(null)
```

4. Directly after the `shortenToRawRange` function, add:

```tsx
  const standardDashboard = edit === null && dashboardId === undefined
  const inspectTarget = activeInspectTarget(search.inspect, { anonymous, standardDashboard })
  const closeInspector = () => {
    void navigate({ search: { ...search, inspect: undefined } })
  }
  const trafficMetric = search.metric === 'pageviews' ? 'pageviews' : 'visitors'
```

5. Replace the standard branch, from `      ) : (` before `<StatsStrip` (line 262) through the matching `      )}` (line 291), with:

```tsx
      ) : (
        <DashboardPanels
          returnFocusRef={inspectorOrigin}
          inspector={inspectTarget ? {
            key: serializeInspect(inspectTarget),
            title: inspectorTitle(inspectTarget),
            dimension: inspectorDimensionLabel(inspectTarget.dimension),
            onClose: closeInspector,
            body: (
              <Inspector
                key={serializeInspect(inspectTarget)}
                anonymous={anonymous}
                query={query}
                target={inspectTarget}
                trafficMetric={trafficMetric}
                onFilter={onFilter}
              />
            ),
          } : undefined}
          main={
            <>
              <StatsStrip
                query={query}
                compare={search.compare}
                onSelectMetric={(metric) => updateSearch({ ...search, metric })}
              />
              <DashboardChart
                anonymous={anonymous}
                canEdit={membership?.role !== 'viewer'}
                compare={search.compare}
                metric={search.metric}
                query={query}
                siteId={site.id}
              />
              <DeferredContent className="min-h-[3000px] lg:min-h-[1600px]" label="Dashboard breakdowns">
                <Suspense fallback={<Skeleton className="size-full min-h-[inherit] rounded-xl" />}>
                  <DashboardBelowFold
                    allowedHosts={site.allowed_hosts}
                    anonymous={anonymous}
                    filters={filters}
                    metric={search.metric}
                    onFilter={onFilter}
                    onShorten={shortenToRawRange}
                    query={query}
                  />
                </Suspense>
              </DeferredContent>
            </>
          }
        />
      )}
```

The editor and viewer branches are untouched. `standardDashboard` is false there, so `inspect` is ignored and no `DashboardPanels` renders (spec section 11.7).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web && bun run test -- src/routes/-dashboard-breakdowns.test.tsx`
Expected: PASS, including every pre-existing test in the file.

- [ ] **Step 5: Measure the JS budget and decide on `resizable`**

Run: `cd web && bun run build 2>&1 | tail -3`
Expected: `Initial JavaScript: N / 256000 bytes gzipped`, where N is about 217932, because `react-resizable-panels` is in the code-split route chunk.

- If the build passes (N ≤ 256000), keep `resizable`. Record N in the commit message body.
- If it fails with `Initial JavaScript is N bytes gzipped; budget is 256000`, apply **Fallback F** (end of this plan) now, then rerun `bun run build` and the tests from Step 4 and Task 4 until both pass.

- [ ] **Step 6: Full check and commit**

Run: `cd web && bun run typecheck && bun run test 2>&1 | grep -E "FAIL|Test Files|Tests "`
Expected: the only failure is the baseline tokens test.

```bash
git add web/src/routes/app.\$org.\$site.index.tsx web/src/routes/-dashboard-breakdowns.test.tsx
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+' || true
git commit -m "feat(web): render the dashboard as main panel plus inspector driven by the inspect param" -m "Initial JavaScript: N / 256000 bytes gzipped."
```

Replace `N` in the message with the measured number. If Fallback F was applied, include its files in `git add` and say "fixed-width inspector (resizable over budget)" in the body.

---

## Group 6b: wire the sources

### Task 7: Inspect on `BreakdownCard` rows

**Files:**
- Create: `web/src/components/InspectButton.tsx`
- Modify: `web/src/components/BreakdownCard.tsx` (props, a helper, the table cell at lines 150-174, the list item at lines 204-235), `web/src/components/DashboardBelowFold.tsx`, `web/src/routes/app.$org.$site.index.tsx`
- Test: `web/src/components/breakdown-card.test.tsx`, `web/src/routes/-dashboard-breakdowns.test.tsx`

**Interfaces:**
- Consumes: `InspectControl`, `inspectTargetFor`, `sameInspect`, `inspectAllowed` and `nextInspect` (Task 2); `ui.inspector.inspectValue` (Task 4); the route's `inspectorOrigin` and `inspectTarget` (Task 6).
- Produces:
  - `InspectButton(props: { label: string; pressed: boolean; onInspect(origin: HTMLElement): void })`
  - `BreakdownCard` gains `inspect?: InspectControl`.
  - `DashboardBelowFold` gains `inspect?: InspectControl`.
  - In the route, `inspectControl: InspectControl | undefined` (`undefined` unless `standardDashboard`).

- [ ] **Step 1: Write the failing unit tests**

Append inside `describe('BreakdownCard', ...)` in `web/src/components/breakdown-card.test.tsx`:

```tsx
  it('offers a separate Inspect control that does not apply the filter', () => {
    useStats.mockReturnValue(result({ data: data('country', 'IN', 5) }))
    const onFilter = vi.fn()
    const open = vi.fn()
    render(<BreakdownCard title="Countries" tabs={tabs('country')} query={baseQuery} onFilter={onFilter} inspect={{ can: () => true, open }} />)

    const inspect = screen.getByRole('button', { name: 'Inspect India' })
    expect(inspect).toHaveAttribute('aria-pressed', 'false')
    fireEvent.click(inspect)
    expect(open).toHaveBeenCalledWith({ dimension: 'country', values: ['IN'] }, inspect)
    expect(onFilter).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'India5' })).toHaveAccessibleDescription('Filter by country India')
  })

  it('marks the inspected place and scopes it to its country', () => {
    useStats.mockReturnValue(result({
      data: { ...data('region', 'California', 5), columns: ['country', 'region', 'visitors'], rows: [{ country: 'US', region: 'California', visitors: 5 }] },
    }))
    render(<BreakdownCard title="Regions" tabs={tabs('region')} query={baseQuery} onFilter={vi.fn()} inspect={{ selected: { dimension: 'region', values: ['California', 'US'] }, can: () => true, open: vi.fn() }} />)

    expect(screen.getByRole('button', { name: 'Inspect California, United States' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('offers Inspect in the page metrics table', () => {
    useStats.mockReturnValue(result({
      data: { columns: ['page', 'visitors', 'pageviews', 'time_on_page', 'exit_rate'], rows: [{ page: '/pricing', visitors: 5, pageviews: 9, time_on_page: 30, exit_rate: 20 }], totals: { visitors: 5 }, meta: { sources_used: ['daily'], cached: false } },
    }))
    const open = vi.fn()
    render(<BreakdownCard title="Pages" tabs={pageTabs} query={baseQuery} onFilter={vi.fn()} inspect={{ can: () => true, open }} />)

    fireEvent.click(screen.getByRole('button', { name: 'Inspect /pricing' }))
    expect(open).toHaveBeenCalledWith({ dimension: 'page', values: ['/pricing'] }, expect.any(HTMLElement))
  })

  it('offers no Inspect where a row has no filter action or the policy blocks it', () => {
    useStats.mockReturnValue(result({ data: data('browser', 'Firefox', 5) }))
    const { unmount } = render(<BreakdownCard title="Browsers" tabs={tabs('browser')} query={baseQuery} onFilter={vi.fn()} inspect={{ can: () => false, open: vi.fn() }} />)
    expect(screen.queryByRole('button', { name: /^Inspect/ })).toBeNull()
    unmount()

    render(<BreakdownCard title="Browsers" tabs={tabs('browser')} query={baseQuery} onFilter={vi.fn()} allowFilter={false} inspect={{ can: () => true, open: vi.fn() }} />)
    expect(screen.queryByRole('button', { name: /^Inspect/ })).toBeNull()
    cleanup()

    useStats.mockReturnValue(result({ data: data('browser_version', '', 5) }))
    render(<BreakdownCard title="Versions" tabs={tabs('browser_version')} query={baseQuery} onFilter={vi.fn()} inspect={{ can: () => true, open: vi.fn() }} />)
    expect(screen.queryByRole('button', { name: /^Inspect/ })).toBeNull()
  })
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && bun run test -- src/components/breakdown-card.test.tsx`
Expected: FAIL with "Unable to find role="button" and name "Inspect India"" (Vitest does not typecheck, so the unknown `inspect` prop is simply ignored). The "offers no Inspect" test already passes.

- [ ] **Step 3: Add `InspectButton`**

Create `web/src/components/InspectButton.tsx`:

```tsx
import { PanelRightOpenIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'

/** Opens the dashboard inspector for one value; separate from the row's filter click. */
export function InspectButton({ label, pressed, onInspect }: { label: string; pressed: boolean; onInspect(origin: HTMLElement): void }) {
  return (
    <Button
      aria-label={label}
      aria-pressed={pressed}
      className="shrink-0 text-muted-foreground aria-pressed:bg-muted aria-pressed:text-foreground"
      size="icon-sm"
      title={label}
      type="button"
      variant="ghost"
      onClick={(event) => onInspect(event.currentTarget)}
    >
      <PanelRightOpenIcon aria-hidden="true" />
    </Button>
  )
}
```

- [ ] **Step 4: Add the Inspect control to `BreakdownCard`**

In `web/src/components/BreakdownCard.tsx`:

1. Add imports after the `@/lib/breakdown-labels` import:

```tsx
import { inspectTargetFor, sameInspect, type InspectControl } from '@/lib/inspect'
import { InspectButton } from './InspectButton'
```

2. Add `inspect?: InspectControl` to `BreakdownCardProps` (after `description?: string`) and to the destructured parameters (`..., description, inspect }: BreakdownCardProps`).

3. Directly after the `maximum` constant, add:

```tsx
  const inspectControl = (dimension: string, label: string, country: string, accessibleLabel: string) => {
    const target = inspectTargetFor(dimension, dimension === 'region' || dimension === 'city' ? [label, country] : [label])
    if (!inspect || !target || !inspect.can(target)) return null
    return <InspectButton label={ui.inspector.inspectValue(accessibleLabel)} pressed={sameInspect(inspect.selected, target)} onInspect={(origin) => inspect.open(target, origin)} />
  }
```

4. In the table branch, replace the filter fragment inside the first `<td>`, from `<>` after `) : (` through `</>` (lines 154-172), with:

```tsx
                        <>
                        <span className="sr-only" id={`${filterDescriptionId}-${index}`}>
                          {ui.breakdown.filterBy(activeTab.dimension, filterAccessibleLabel(activeTab.dimension, label, display, placeCountry))}
                        </span>
                        <div className="flex min-w-0 items-center gap-1">
                          <button
                            aria-describedby={`${filterDescriptionId}-${index}`}
                            className="min-w-0 flex-1 rounded-lg px-2 py-2 text-left outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring"
                            type="button"
                            onClick={() => onFilter({
                              dimension: activeTab.dimension,
                              operator: 'is',
                              values: activeTab.dimension === 'region' || activeTab.dimension === 'city'
                                ? [label, placeCountry]
                                : [label],
                            })}
                          >
                            <span className="block min-w-0 truncate font-medium" title={display}>{display}</span>
                          </button>
                          {inspectControl(activeTab.dimension, label, placeCountry, filterAccessibleLabel(activeTab.dimension, label, display, placeCountry))}
                        </div>
                        </>
```

5. In the list branch, replace the filter fragment inside `<li>`, from `<>` after `) : (` through `</>` (lines 205-234), with:

```tsx
                  <>
                  <span className="sr-only" id={`${filterDescriptionId}-${index}`}>
                    {ui.breakdown.filterBy(activeTab.dimension, filterAccessibleLabel(activeTab.dimension, label, display, country))}
                  </span>
                  <div className="flex min-w-0 items-center gap-1">
                    <button
                      aria-describedby={`${filterDescriptionId}-${index}`}
                      className="relative grid min-w-0 flex-1 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-lg px-2 py-2 text-left outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring"
                      type="button"
                      onClick={() =>
                        onFilter({
                          dimension: activeTab.dimension,
                          operator: 'is',
                          values: activeTab.dimension === 'region' || activeTab.dimension === 'city'
                            ? [label, country]
                            : [label],
                        })
                      }
                    >
                      <span aria-hidden="true" className="absolute inset-y-1 left-0 min-w-1.5 rounded-sm bg-primary/10 dark:bg-primary/15" style={{ width }} />
                      <span className="relative flex min-w-0 items-center gap-2 text-sm">
                        {activeTab.dimension === 'country' || activeTab.dimension === 'region' || activeTab.dimension === 'city'
                          ? <Flag code={country} />
                          : null}
                        <span className="min-w-0 truncate" title={display}>{display}</span>
                      </span>
                      <span className="relative text-sm tabular-nums text-foreground">
                        {fmt.number(value)}
                      </span>
                    </button>
                    {inspectControl(activeTab.dimension, label, country, filterAccessibleLabel(activeTab.dimension, label, display, country))}
                  </div>
                  </>
```

The only class changes on the filter buttons are `w-full` becoming `flex-1`. The non-filterable branches (empty label, `allowFilter=false`, place without country) are untouched, so they get no Inspect control.

- [ ] **Step 5: Pass `inspect` through `DashboardBelowFold`**

In `web/src/components/DashboardBelowFold.tsx`:
- Add `import type { InspectControl } from '@/lib/inspect'`.
- Add `inspect,` to the destructured props and `inspect?: InspectControl` to the props type.
- In `renderBreakdownCard`'s `<BreakdownCard ... />`, add `inspect={inspect}` after `onFilter={onFilter}`.

- [ ] **Step 6: Run the unit tests to verify they pass**

Run: `cd web && bun run test -- src/components/breakdown-card.test.tsx`
Expected: PASS, including every existing BreakdownCard test (`India5`, `California5` and `Springfield5` keep their names and descriptions).

- [ ] **Step 7: Write the failing route test**

Append to `web/src/routes/-dashboard-breakdowns.test.tsx`:

```tsx
  it('opens the inspector from a breakdown row without filtering and returns focus to Inspect on close', async () => {
    const router = renderAt(range)
    const location = await screen.findByRole('region', { name: 'Location' })
    const inspect = await within(location).findByRole('button', { name: 'Inspect India' })
    fireEvent.click(inspect)
    await waitFor(() => expect(router.state.location.search.inspect).toBe('country:IN'))
    expect(router.state.location.search.f).toEqual([])

    const inspector = await screen.findByRole('dialog', { name: 'India' })
    fireEvent.keyDown(inspector, { key: 'Escape' })
    await waitFor(() => expect(router.state.location.search.inspect).toBeUndefined())
    await waitFor(() => expect(inspect).toHaveFocus())
  })
```

Run: `cd web && bun run test -- src/routes/-dashboard-breakdowns.test.tsx -t "opens the inspector from a breakdown row"`
Expected: FAIL ("Unable to find role="button" and name "Inspect India"", because the route does not pass `inspect` yet).

- [ ] **Step 8: Build the inspect control in the route**

In `web/src/routes/app.$org.$site.index.tsx`:

1. Extend the inspect import to `import { activeInspectTarget, inspectAllowed, nextInspect, parseInspectSearch, serializeInspect, withoutInspect, type InspectControl } from '../lib/inspect'`.
2. Directly after the `trafficMetric` constant (Task 6), add:

```tsx
  const inspectControl: InspectControl | undefined = standardDashboard ? {
    selected: inspectTarget,
    can: (target) => inspectAllowed(target, anonymous),
    open: (target, origin) => {
      inspectorOrigin.current = origin
      void navigate({ search: { ...search, inspect: nextInspect(inspectTarget, target) } })
    },
  } : undefined
```

3. In the standard branch's `<DashboardBelowFold ... />`, add `inspect={inspectControl}` after `filters={filters}`.

- [ ] **Step 9: Run the tests to verify they pass**

Run: `cd web && bun run test -- src/routes/-dashboard-breakdowns.test.tsx src/components/breakdown-card.test.tsx`
Expected: PASS, including the existing `clicking a country row adds the is filter to router search` (the filter button keeps its description).

- [ ] **Step 10: Full check and commit**

Run: `cd web && bun run typecheck && bun run test 2>&1 | grep -E "FAIL|Test Files|Tests "`
Expected: the only failure is the baseline tokens test.

```bash
git add web/src/components/InspectButton.tsx web/src/components/BreakdownCard.tsx web/src/components/DashboardBelowFold.tsx web/src/routes/app.\$org.\$site.index.tsx web/src/components/breakdown-card.test.tsx web/src/routes/-dashboard-breakdowns.test.tsx
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+' || true
git commit -m "feat(web): add Inspect to breakdown rows, separate from the filter click"
```

### Task 8: Inspect on Where countries and Weekly rhythm cells

**Files:**
- Modify: `web/src/features/map/CountryTable.tsx`, `web/src/features/map/WhereCard.tsx`, `web/src/features/map/TrafficGrid.tsx`, `web/src/features/map/WeeklyRhythmCard.tsx`, `web/src/components/DashboardBelowFold.tsx`
- Test: `web/src/features/map/CountryTable.test.tsx`, `web/src/features/map/TrafficGrid.test.tsx`, `web/src/routes/-dashboard-breakdowns.test.tsx`

**Interfaces:**
- Consumes: `InspectControl`, `inspectTargetFor` and `sameInspect` (Task 2); `InspectButton` (Task 7); `ui.inspector.inspectValue` and `ui.inspector.cellHint` (Task 4).
- Produces: an optional `inspect?: InspectControl` prop on `CountryTable`, `WhereCard`, `TrafficGrid` and `WeeklyRhythmCard`. Without it, all four render exactly as today.

- [ ] **Step 1: Write the failing component tests**

Append inside `describe('CountryTable', ...)` in `web/src/features/map/CountryTable.test.tsx`:

```tsx
  it('offers Inspect per country without selecting it, and only where allowed', () => {
    const onSelect = vi.fn()
    const open = vi.fn()
    render(
      <CountryTable
        rows={rows.slice(0, 2)}
        metric="visitors"
        onActiveChange={vi.fn()}
        onSelect={onSelect}
        inspect={{ selected: { dimension: 'country', values: ['IN'] }, can: (target) => target.values[0] !== 'US', open }}
      />,
    )

    expect(screen.queryByRole('button', { name: 'Inspect United States' })).toBeNull()
    const india = screen.getByRole('button', { name: 'Inspect India' })
    expect(india).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(india)
    expect(open).toHaveBeenCalledWith({ dimension: 'country', values: ['IN'] }, india)
    expect(onSelect).not.toHaveBeenCalled()
  })
```

Append inside `describe('TrafficGrid', ...)` in `web/src/features/map/TrafficGrid.test.tsx`:

```tsx
  it('inspects a cell on activation and marks the inspected cell', () => {
    const open = vi.fn()
    render(<TrafficGrid dateRange={{ from: '2026-09-28', to: '2026-09-30' }} metric="visitors" rows={[
      { weekday: 1, hour_of_day: 13, visitors: 89 },
    ]} timezone="UTC" locale="en-GB" inspect={{ selected: { dimension: 'weekday_hour', values: ['1', '13'] }, can: () => true, open }} />)

    const cell = screen.getByRole('gridcell', { name: 'Monday 13:00: 89 visitors' })
    expect(cell).toHaveAttribute('aria-selected', 'true')
    expect(cell).toHaveAccessibleDescription('Select a cell to inspect it.')
    expect(screen.getByRole('gridcell', { name: 'Monday 14:00: 0 visitors' })).toHaveAttribute('aria-selected', 'false')
    fireEvent.click(cell)
    expect(open).toHaveBeenCalledWith({ dimension: 'weekday_hour', values: ['1', '13'] }, cell)
  })

  it('keeps cells inert without an inspect control', () => {
    render(<TrafficGrid dateRange={{ from: '2026-09-28', to: '2026-09-30' }} metric="visitors" rows={[
      { weekday: 1, hour_of_day: 13, visitors: 89 },
    ]} timezone="UTC" locale="en-GB" />)

    const cell = screen.getByRole('gridcell', { name: 'Monday 13:00: 89 visitors' })
    expect(cell).not.toHaveAttribute('aria-selected')
    expect(cell).not.toHaveAttribute('aria-describedby')
  })
```

Append to `web/src/routes/-dashboard-breakdowns.test.tsx`:

```tsx
  it('opens the inspector from a Where country and from a Weekly rhythm cell', async () => {
    const router = renderAt(range)
    const where = await screen.findByRole('region', { name: 'Where' })
    fireEvent.click(await within(where).findByRole('button', { name: 'Inspect India' }, { timeout: 5_000 }))
    await waitFor(() => expect(router.state.location.search.inspect).toBe('country:IN'))
    fireEvent.keyDown(await screen.findByRole('dialog', { name: 'India' }), { key: 'Escape' })
    await waitFor(() => expect(router.state.location.search.inspect).toBeUndefined())

    fireEvent.click(screen.getByRole('gridcell', { name: 'Monday 1 PM: 89 visitors' }))
    await waitFor(() => expect(router.state.location.search.inspect).toBe('weekday_hour:1,13'))
    const cell = await screen.findByRole('dialog', { name: 'Monday 1 PM' })
    expect(await within(cell).findByText('89')).toBeVisible()
    expect(within(cell).queryByRole('button', { name: 'Add as filter' })).toBeNull()
    expect(router.state.location.search.f).toEqual([])
  }, 15_000)
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && bun run test -- src/features/map/CountryTable.test.tsx src/features/map/TrafficGrid.test.tsx src/routes/-dashboard-breakdowns.test.tsx`
Expected: FAIL in the two component inspect tests ("Unable to find role="button" and name "Inspect India"", and `aria-selected` missing) and in the new route test (the "Inspect India" button inside Where times out). The inert test passes.

- [ ] **Step 3: Add Inspect to `CountryTable`**

In `web/src/features/map/CountryTable.tsx`:
- Add imports:

```tsx
import { InspectButton } from '@/components/InspectButton'
import { inspectTargetFor, sameInspect, type InspectControl } from '@/lib/inspect'
import { ui } from '@/messages/ui'
```

- Add `inspect?: InspectControl` to `CountryTableProps`, and `inspect` to the destructured parameters.
- Replace the `<li ...>` opening tag:

```tsx
            <li key={row.country} className="grid grid-cols-[1.25rem_minmax(0,1fr)_auto] items-center gap-1.5">
```

with:

```tsx
            <li key={row.country} className={cn('grid items-center gap-1.5', inspect ? 'grid-cols-[1.25rem_minmax(0,1fr)_auto_auto]' : 'grid-cols-[1.25rem_minmax(0,1fr)_auto]')}>
```

- Directly after `<span className="text-right text-sm tabular-nums">{fmt.number(value)}</span>` inside that `<li>`, add:

```tsx
              {(() => {
                const target = inspectTargetFor('country', [row.country])
                if (!inspect || !target || !inspect.can(target)) return null
                return <InspectButton label={ui.inspector.inspectValue(label)} pressed={sameInspect(inspect.selected, target)} onInspect={(origin) => inspect.open(target, origin)} />
              })()}
```

- [ ] **Step 4: Pass it through `WhereCard`**

In `web/src/features/map/WhereCard.tsx`:
- Add `import type { InspectControl } from '@/lib/inspect'`.
- Add `inspect?: InspectControl` to `WhereCardProps` and change the signature to `export function WhereCard({ query, metric, onFilter, inspect }: WhereCardProps)`.
- In `<CountryTable ... />`, add `inspect={inspect}` after `activeCountry={activeCountry}`.

- [ ] **Step 5: Make Weekly rhythm cells inspectable**

In `web/src/features/map/TrafficGrid.tsx`:
- Add imports:

```tsx
import { inspectTargetFor, sameInspect, type InspectControl } from '@/lib/inspect'
import { ui } from '@/messages/ui'
```

- Add `inspect?: InspectControl` to `TrafficGridProps`, and `inspect` to the destructured parameters of `TrafficGrid`.
- Directly after `const summaryId = useId()`, add `const inspectHintId = useId()`.
- Directly after `<p className="sr-only" id={summaryId}>{dataSummary}</p>`, add:

```tsx
        {inspect ? <p className="sr-only" id={inspectHintId}>{ui.inspector.cellHint}</p> : null}
```

- Inside the cell `map`, directly after `const busiest = ...`, add:

```tsx
                    const target = inspectTargetFor('weekday_hour', [String(cell.weekday), String(cell.hour)])
                    const inspectTarget = target && inspect?.can(target) ? target : undefined
```

- On the cell `<button>`, add these props after `aria-label={label}`:

```tsx
                        aria-describedby={inspectTarget ? inspectHintId : undefined}
                        aria-selected={inspectTarget ? sameInspect(inspect?.selected, inspectTarget) : undefined}
                        onClick={inspectTarget ? (event) => inspect?.open(inspectTarget, event.currentTarget) : undefined}
```

In `web/src/features/map/WeeklyRhythmCard.tsx`:
- Add `import type { InspectControl } from '@/lib/inspect'`.
- Change the signature to:

```tsx
export function WeeklyRhythmCard({ query, metric, onShorten, inspect }: {
  query: StatsQuery
  metric: TrafficMetric
  onShorten(days: number): void
  inspect?: InspectControl
}) {
```

- Add `inspect={inspect}` to `<TrafficGrid ... />` after `error={traffic.error}`.

In `web/src/components/DashboardBelowFold.tsx`, add `inspect={inspect}` to `<WhereCard ... />` and to `<WeeklyRhythmCard ... />`.

- [ ] **Step 6: Run the component tests to verify they pass**

Run: `cd web && bun run test -- src/features/map`
Expected: PASS, including the existing `CountryTable`, `TrafficGrid`, `WhereCard` and `CountryDetails` tests.

- [ ] **Step 7: Run the route test to verify it passes**

Run: `cd web && bun run test -- src/routes/-dashboard-breakdowns.test.tsx`
Expected: PASS, including the existing `renders Where and Weekly rhythm ...` test (`India, rank 1` and `Filter the dashboard to this country` are unchanged).

- [ ] **Step 8: Full check and commit**

Run: `cd web && bun run typecheck && bun run test 2>&1 | grep -E "FAIL|Test Files|Tests "`
Expected: the only failure is the baseline tokens test.

```bash
git add web/src/features/map/CountryTable.tsx web/src/features/map/WhereCard.tsx web/src/features/map/TrafficGrid.tsx web/src/features/map/WeeklyRhythmCard.tsx web/src/components/DashboardBelowFold.tsx web/src/features/map/CountryTable.test.tsx web/src/features/map/TrafficGrid.test.tsx web/src/routes/-dashboard-breakdowns.test.tsx
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+' || true
git commit -m "feat(web): add Inspect to Where countries and Weekly rhythm cells"
```

### Task 9: Inspector e2e spec (edited, not run), the spec note, and final verification

**Files:**
- Create: `web/e2e/inspector.spec.ts`
- Modify: `docs/superpowers/specs/2026-10-13-appshell-dashboard-design.md` (section 11.7, chart bullet)

**Interfaces:**
- Consumes: everything above. Selectors: `Inspect India`, `Close inspector`, `Resize inspector`, `complementary`/`dialog` named by the value, and storage key `supermetry:inspector-width`.

- [ ] **Step 1: Write the e2e spec (edited, not run)**

Create `web/e2e/inspector.spec.ts`:

```ts
import { expect, test } from './fixtures'

const range = '?period=custom&from=2026-09-28&to=2026-09-30'

test('Inspect opens the inspector without filtering, survives reload, resizes, and closes with Esc and Back', async ({ page, owner }) => {
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}${range}`
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, dashboard)

  const inspect = page.getByRole('region', { name: 'Location' }).getByRole('button', { name: 'Inspect India' })
  await inspect.click()
  await expect.poll(() => new URL(page.url()).searchParams.get('inspect')).toBe('country:IN')
  expect(new URL(page.url()).searchParams.getAll('f')).toEqual([])
  const inspector = page.getByRole('complementary', { name: 'India' })
  await expect(inspector).toBeVisible()
  await expect(inspector.getByRole('heading', { name: 'India' })).toBeFocused()
  await expect(inspector.getByRole('region', { name: 'Metrics for this value' })).toBeVisible()

  const handle = page.getByRole('separator', { name: 'Resize inspector' })
  await handle.focus()
  await page.keyboard.press('ArrowLeft')
  await expect.poll(() => page.evaluate(() => Number(window.localStorage.getItem('supermetry:inspector-width')))).toBeGreaterThan(24)

  await page.reload()
  await expect(page.getByRole('complementary', { name: 'India' })).toBeVisible()
  await page.getByRole('complementary', { name: 'India' }).getByRole('button', { name: 'Close inspector' }).focus()
  await page.keyboard.press('Escape')
  await expect.poll(() => new URL(page.url()).searchParams.get('inspect')).toBeNull()
  await expect(page.getByRole('complementary', { name: 'India' })).toHaveCount(0)

  await page.getByRole('region', { name: 'Location' }).getByRole('button', { name: 'Inspect India' }).click()
  await expect(page.getByRole('complementary', { name: 'India' })).toBeVisible()
  await page.goBack()
  await expect(page.getByRole('complementary', { name: 'India' })).toHaveCount(0)
})

test('the inspector is a sheet below 1280px and a bottom sheet at 390px without horizontal scroll', async ({ page, owner }) => {
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}${range}&inspect=country%3AIN`
  await page.setViewportSize({ width: 1024, height: 900 })
  await owner.login(page, dashboard)
  await expect(page.getByRole('dialog', { name: 'India' })).toHaveAttribute('data-side', 'right')

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole('dialog', { name: 'India' })).toHaveAttribute('data-side', 'bottom')
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth))
    .toBeLessThanOrEqual(0)
  await page.keyboard.press('Escape')
  await expect.poll(() => new URL(page.url()).searchParams.get('inspect')).toBeNull()
})
```

If Fallback F was applied in Task 6, delete the `handle` block (the four lines from `const handle` to the `expect.poll` on `localStorage`) and add `await expect(page.getByRole('separator', { name: 'Resize inspector' })).toHaveCount(0)` in its place.

- [ ] **Step 2: Record the chart-point decision in the spec**

In `docs/superpowers/specs/2026-10-13-appshell-dashboard-design.md`, replace the bullet:

```markdown
- **Chart point inspection** has no existing detail view; ship it last, or drop it if the per-day data would need a new query.
```

with:

```markdown
- **Chart point inspection: dropped (2026-10-13).** `MainChart` fetches one metric per bucket from `/stats/timeseries`, and buckets may be hours, weeks or months. A point's own metrics would need a new per-bucket query, so the inspector opens from breakdown rows, Where countries and Weekly rhythm cells only.
```

- [ ] **Step 3: Final verification**

Run:

```bash
cd web && bun run typecheck && bun run test 2>&1 | grep -E "FAIL|Test Files|Tests " && bun run build 2>&1 | tail -1
```

Expected: typecheck passes; the only failure is the baseline `packages/tokens` test; the last line reads `Initial JavaScript: N / 256000 bytes gzipped` with N ≤ 256000. Do not run Playwright here (the webServer cannot log in to the DB). Note in the PR that `e2e/inspector.spec.ts` and `e2e/dashboard.spec.ts` are edited, not run.

- [ ] **Step 4: Commit**

```bash
git add web/e2e/inspector.spec.ts docs/superpowers/specs/2026-10-13-appshell-dashboard-design.md
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+' || true
git commit -m "test(web): add the inspector e2e spec and record dropping chart point inspection" -m "e2e/inspector.spec.ts is edited, not run: the e2e webServer cannot log in to the database here."
```

---

## Fallback F: fixed-width inspector (only if Task 6 Step 5 exceeds the budget)

Spec section 11.4: "if not, use a fixed-width inspector with no drag handle and drop the resize criterion."

1. Remove the dependency and the width code:

```bash
cd web && bun remove react-resizable-panels
git rm web/src/components/ui/resizable.tsx web/src/components/inspectorWidth.ts web/src/components/inspectorWidth.test.ts
```

2. In `web/src/components/DashboardPanels.tsx`:
   - Delete the imports of `@/components/ui/resizable` and `./inspectorWidth`, and the line `const [width] = useState(readInspectorWidth)`. Change the React import to `import { useEffect, useId, useRef, type KeyboardEvent, type ReactNode, type RefObject } from 'react'`.
   - Replace the whole `if (wide) { ... }` block with:

```tsx
  if (wide) {
    return (
      <div className={inspector ? 'grid min-w-0 grid-cols-[minmax(40rem,1fr)_24rem] items-start gap-4' : 'grid min-w-0 grid-cols-1'}>
        <div className="flex min-w-0 flex-col gap-4">{main}</div>
        {inspector ? (
          <aside
            aria-labelledby={headingId}
            className="sticky top-28 flex max-h-[calc(100dvh-7.5rem)] min-w-0 flex-col overflow-y-auto rounded-xl border bg-card text-card-foreground"
            onKeyDown={closeOnEscape}
          >
            <header className="flex items-start justify-between gap-2 border-b p-4">
              <div className="flex min-w-0 flex-col gap-0.5">
                <p className="text-xs text-muted-foreground">{inspector.dimension}</p>
                <h2 ref={headingRef} className="break-words text-sm font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring" id={headingId} tabIndex={-1}>
                  {inspector.title}
                </h2>
              </div>
              <Button aria-label={ui.inspector.close} size="icon-sm" type="button" variant="ghost" onClick={inspector.onClose}>
                <XIcon />
              </Button>
            </header>
            {inspector.body}
          </aside>
        ) : null}
      </div>
    )
  }
```

   Main stays the first child in both states, so it is not remounted when the inspector opens.

3. In `web/src/components/DashboardPanels.test.tsx`, replace `expect(screen.getByRole('separator', { name: 'Resize inspector' })).toBeInTheDocument()` with `expect(screen.queryByRole('separator')).toBeNull()`. Delete the `ResizeObserverStub` class, its `vi.stubGlobal` line and `vi.unstubAllGlobals()`.
4. Remove `resize` from `ui.inspector` in `ui.ts`, `de.ui.ts` and `es.ui.ts`, so no unused string remains.
5. Rerun `bun run test -- src/components/DashboardPanels.test.tsx src/messages/locales/locales.test.ts src/routes/-dashboard-breakdowns.test.tsx` and `bun run build`. Both must pass.
