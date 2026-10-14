# AppShell Frame Implementation Plan (steps 1-3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every page one shared frame (`PageContainer`, `PageHeader`) and give the header breadcrumbs, without changing any page's data or behaviour.

**Architecture:** Two presentational components in `web/src/components/` carry the title, description, actions and width. A pure `buildCrumbs(pathname, membership)` function maps a location to crumbs, and a thin `Breadcrumbs` component renders them with the shadcn `breadcrumb` primitive inside the existing header. A guard test lists every route or feature file that still hand-rolls an `<h1>`, and each adoption batch shrinks that list.

**Tech Stack:** React 19, TypeScript, TanStack Router and Query, shadcn/ui (Radix), Tailwind v4, Vitest + Testing Library, Playwright, Bun.

**Spec:** `docs/superpowers/specs/2026-10-13-appshell-dashboard-design.md` (sections 2-5 and 8 steps 1-3). Steps 4-8 and the section 11 inspector get their own plans after this one lands, written against the code it leaves behind.

## Global Constraints

- Scope is `web/` only. No backend change, no new endpoint.
- Use Bun: `bun install --frozen-lockfile`, `bun run <script>`, `bunx`. Never npm.
- Client-only SPA. No full-stack framework, no Node server.
- JS budget 256 KB gzip, enforced by `bun run build` (`scripts/size-check.mjs`).
- No new hardcoded English: strings go in `web/src/messages/ui.ts` (registered catalogue `ui`), read at render time.
- Use existing tokens. No new colours.
- Page title is `text-xl font-semibold tracking-tight`. Analytics pages are wide (`max-w-screen-2xl`), settings and account pages are narrow (`max-w-5xl`).
- Exactly one `main` landmark with `id="main-content"`. Do not add another.
- Commit identity is `velixio <270455167+velixio@users.noreply.github.com>`. Commit messages describe the change only: no trailers, no tool names. Before every commit run `git diff --cached | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'` and stop on any match other than the noreply address.
- One PR per task group below, from its own branch, never pushed to main directly. Do not push unless the user asks.
- Deviation from spec section 4: `PageContainer` carries width and gap only. The `p-4 md:p-6` padding stays on `<main>` in `AppShell`, because pages adopt the frame one at a time and padding in the container would double up during the transition.

## Review Focus

- Very long titles or descriptions (a 120-character funnel name): the title wraps or truncates inside `PageHeader` and never pushes actions off screen.
- `actions` absent: no empty actions wrapper is rendered (no stray gap).
- Organisation with zero sites: breadcrumbs show only `Org / Page`, no empty site crumb.
- Site slug in the URL that is not in the membership (stale link): the crumb falls back to the slug and does not crash.
- Detail routes with an id segment (`/funnels/42`, `/profiles/abc`): the id is never shown as a crumb label; the crumb reads "Detail".
- Unknown trailing segment: shown as the raw segment, never an empty crumb or a thrown error.
- Mobile (<768 px): only the last two crumbs render, and the header does not overflow.
- Viewer role: the dashboard still renders without the Manage control, and `PageHeader` actions collapse cleanly.

---

## File Structure

Create:
- `web/src/components/PageContainer.tsx` - width and vertical rhythm wrapper.
- `web/src/components/PageHeader.tsx` - `<header>` with the single `<h1>`, description, actions.
- `web/src/components/PageHeader.test.tsx` - tests for both.
- `web/src/components/ui/breadcrumb.tsx` - installed by shadcn.
- `web/src/components/routeCrumbs.ts` - pure `buildCrumbs`.
- `web/src/components/routeCrumbs.test.ts`
- `web/src/components/Breadcrumbs.tsx`
- `web/src/components/Breadcrumbs.test.tsx`
- `web/src/routes/-page-frame-adoption.test.ts` - the h1 guard.

Modify:
- `web/src/messages/ui.ts` - add `pageFrame` and `crumbs` strings.
- `web/src/components/AppShell.tsx` - wire `Breadcrumbs` into the header (Task 8 only).
- `web/src/routes/app.$org.$site.index.tsx` - adopt the frame (Task 2).
- About 40 route and feature files - adopt the frame (Tasks 3-7).

Branches: `feat/appshell-frame-1-primitives` (Tasks 1-2), `feat/appshell-frame-2-adopt` (Tasks 3-7), `feat/appshell-frame-3-breadcrumbs` (Tasks 8-9). Branch each from `main` once the previous PR is merged. The spec is on `docs/appshell-inspector-spec`; merge that PR first, or branch from it.

Baseline check before starting (record the output, do not fix unrelated failures):

```bash
cd web && bun install --frozen-lockfile && bun run typecheck && bun run test 2>&1 | tail -15
```

---

## Group 1: frame primitives (branch `feat/appshell-frame-1-primitives`)

### Task 1: PageContainer and PageHeader

**Files:**
- Create: `web/src/components/PageContainer.tsx`, `web/src/components/PageHeader.tsx`
- Test: `web/src/components/PageHeader.test.tsx`

**Interfaces:**
- Produces:
  - `PageContainer(props: { width?: 'wide' | 'narrow'; className?: string; children: ReactNode })`. Default `width` is `'wide'`.
  - `PageHeader(props: { title: ReactNode; description?: ReactNode; actions?: ReactNode; className?: string })`. Renders exactly one `<h1>`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/PageHeader.test.tsx`:

```tsx
import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { PageContainer } from './PageContainer'
import { PageHeader } from './PageHeader'

afterEach(cleanup)

describe('PageHeader', () => {
  it('renders one level-one heading with the standard title style', () => {
    render(<PageHeader title="Funnels" />)
    const heading = screen.getByRole('heading', { level: 1, name: 'Funnels' })
    expect(heading).toHaveClass('text-xl', 'font-semibold', 'tracking-tight')
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1)
  })

  it('renders the description and actions when given', () => {
    render(<PageHeader title="Goals" description="Last 30 days" actions={<button type="button">New goal</button>} />)
    expect(screen.getByText('Last 30 days')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New goal' })).toBeInTheDocument()
  })

  it('renders no description or actions wrapper when they are absent', () => {
    const { container } = render(<PageHeader title="Goals" />)
    expect(container.querySelector('p')).toBeNull()
    expect(container.querySelector('[data-slot="page-header-actions"]')).toBeNull()
  })

  it('lets a very long title wrap without hiding the actions', () => {
    const title = 'A'.repeat(120)
    render(<PageHeader title={title} actions={<button type="button">Act</button>} />)
    expect(screen.getByRole('heading', { level: 1 })).toHaveClass('break-words')
    expect(screen.getByRole('button', { name: 'Act' })).toBeInTheDocument()
  })
})

describe('PageContainer', () => {
  it('is wide by default and narrow on request', () => {
    const { rerender, container } = render(<PageContainer>x</PageContainer>)
    expect(container.firstElementChild).toHaveClass('max-w-screen-2xl', 'gap-4', 'min-w-0')
    rerender(<PageContainer width="narrow">x</PageContainer>)
    expect(container.firstElementChild).toHaveClass('max-w-5xl')
  })

  it('merges a caller class name', () => {
    const { container } = render(<PageContainer className="extra">x</PageContainer>)
    expect(container.firstElementChild).toHaveClass('extra')
  })
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && bun run test -- src/components/PageHeader.test.tsx`
Expected: FAIL, "Failed to resolve import './PageContainer'".

- [ ] **Step 3: Write the implementation**

Create `web/src/components/PageContainer.tsx`:

```tsx
import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

const widths = {
  wide: 'max-w-screen-2xl',
  narrow: 'max-w-5xl',
} as const

/** Width and vertical rhythm for a page. Padding stays on `<main>` in AppShell. */
export function PageContainer({
  width = 'wide',
  className,
  children,
}: {
  width?: keyof typeof widths
  className?: string
  children: ReactNode
}) {
  return <div className={cn('mx-auto flex w-full min-w-0 flex-col gap-4', widths[width], className)}>{children}</div>
}
```

Create `web/src/components/PageHeader.tsx`:

```tsx
import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

/** The single h1 of a page, with an optional description and actions. */
export function PageHeader({
  title,
  description,
  actions,
  className,
}: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
  className?: string
}) {
  return (
    <header className={cn('flex flex-wrap items-end justify-between gap-x-4 gap-y-3', className)}>
      <div className="flex min-w-0 flex-col gap-1">
        <h1 className="break-words text-xl font-semibold tracking-tight">{title}</h1>
        {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
      </div>
      {actions ? (
        <div className="flex flex-wrap items-center gap-2" data-slot="page-header-actions">
          {actions}
        </div>
      ) : null}
    </header>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && bun run test -- src/components/PageHeader.test.tsx`
Expected: PASS, 6 tests.

- [ ] **Step 5: Commit**

```bash
git checkout -b feat/appshell-frame-1-primitives
git add web/src/components/PageContainer.tsx web/src/components/PageHeader.tsx web/src/components/PageHeader.test.tsx
# run the email scan from Global Constraints, then:
git commit -m "feat(web): add PageContainer and PageHeader frame primitives"
```

### Task 2: Adopt the frame on the dashboard

**Files:**
- Modify: `web/src/routes/app.$org.$site.index.tsx` (the returned JSX, lines ~157-183, and the closing tag at the end)
- Test: existing `web/src/routes/-dashboard-breakdowns.test.tsx` and any test asserting the dashboard subtitle

**Interfaces:**
- Consumes: `PageContainer`, `PageHeader` from Task 1.

- [ ] **Step 1: Find tests that depend on the old header**

Run: `cd web && grep -rln "site.name\|data-filter-count\|dashboard-include-imported" src/routes/*.test.tsx src/components/*.test.tsx e2e | head`
Expected: a short list. Note which assert the `Site, date – date` subtitle. Per spec section 5.1 the site name leaves the subtitle (it is in the breadcrumbs from Task 8), so those assertions change to the date range only.

- [ ] **Step 2: Update the failing assertion first**

In each test found in Step 1 that asserts the subtitle with the site name, change the expected text to the date range alone (`fmt.date(from) – fmt.date(to)`). Run the test and confirm it FAILS for that reason:

Run: `cd web && bun run test -- src/routes/-dashboard-breakdowns.test.tsx`
Expected: FAIL on the subtitle assertion (if no test asserts it, skip to Step 3).

- [ ] **Step 3: Replace the hand-rolled header**

In `web/src/routes/app.$org.$site.index.tsx`, add imports:

```tsx
import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
```

Replace the outer `<div className="flex min-w-0 flex-col gap-4" data-filter-count={filters.length}>` and the whole `<header>...</header>` block with:

```tsx
    <PageContainer>
      <div className="contents" data-filter-count={filters.length}>
      <PageHeader
        title={en.navigation.dashboard}
        description={`${fmt.date(query.dateRange.from, timezone)} – ${fmt.date(query.dateRange.to, timezone)}`}
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
            <Field orientation="horizontal" className="w-auto min-h-9 gap-2">
              <Checkbox id="dashboard-include-imported" checked={search.include_imported} onCheckedChange={(checked) => updateSearch({ ...search, include_imported: checked === true })} />
              <FieldLabel htmlFor="dashboard-include-imported" className="text-sm text-muted-foreground">{en.polish.includeImported}</FieldLabel>
            </Field>
          </>
        }
      />
```

`PageContainer` does not accept `data-filter-count`, so it moves to the `contents` wrapper shown above (`display: contents` keeps the flex gap working). Replace the final `</div>` of the component with `</div></PageContainer>`. The existing `-dashboard-breakdowns` test or e2e reads `data-filter-count`; confirm with `grep -rn data-filter-count web/src web/e2e`.

- [ ] **Step 4: Run typecheck and tests**

Run: `cd web && bun run typecheck && bun run test -- src/routes src/components/dashboard-state.test.tsx 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 5: Run the dashboard e2e**

Run: `cd web && bunx playwright test e2e/dashboard.spec.ts 2>&1 | tail -15`
Expected: PASS. If it fails on header text, update that selector to the new structure (one `h1` "Dashboard").

- [ ] **Step 6: Commit and open the PR (ask the user before pushing)**

```bash
git add -A web/src
# email scan, then:
git commit -m "feat(web): adopt PageHeader and PageContainer on the dashboard"
```

---

## Group 2: adopt everywhere (branch `feat/appshell-frame-2-adopt`)

### Task 3: Guard test that lists every hand-rolled h1

**Files:**
- Create: `web/src/routes/-page-frame-adoption.test.ts`

**Interfaces:**
- Produces: `PENDING` (a `Set<string>` of repo-relative paths) at the top of the test file. Tasks 4-7 delete entries from it. `OUTSIDE_SHELL` (a `Set<string>`) lists files that legitimately keep their own h1.

- [ ] **Step 1: Generate the current list**

Run: `cd web && grep -rl '<h1' src/routes src/features --include=*.tsx | grep -v '\.test\.' | sort`
Expected: about 40 paths. Keep this output; it seeds `PENDING`.

- [ ] **Step 2: Write the guard test**

Create `web/src/routes/-page-frame-adoption.test.ts` (replace the placeholder entries with the Step 1 output, paths relative to `web/`, minus the entries you move into `OUTSIDE_SHELL`):

```ts
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = join(__dirname, '..', '..') // web/

// Files that render outside AppShell (sign-in, no-org screen, public share):
// they own their layout and keep a hand-rolled h1.
const OUTSIDE_SHELL = new Set<string>([
  'src/features/auth/AuthScreen.tsx',
  'src/features/lifecycle/NoOrgScreen.tsx',
  'src/features/share/SharedDashboard.tsx',
  'src/routes/app.index.tsx',
])

// Files still to migrate. Each adoption task deletes its entries.
const PENDING = new Set<string>([
  // paste the Step 1 list here, one path per line
])

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return sourceFiles(path)
    return /\.tsx$/.test(name) && !/\.test\./.test(name) ? [path] : []
  })
}

describe('page frame adoption', () => {
  const withH1 = [...sourceFiles(join(root, 'src/routes')), ...sourceFiles(join(root, 'src/features'))]
    .filter((file) => readFileSync(file, 'utf8').includes('<h1'))
    .map((file) => relative(root, file))

  it('has no hand-rolled h1 outside the allowlists', () => {
    const unexpected = withH1.filter((file) => !OUTSIDE_SHELL.has(file) && !PENDING.has(file))
    expect(unexpected).toEqual([])
  })

  it('keeps PENDING free of files that already migrated', () => {
    const stale = [...PENDING].filter((file) => !withH1.includes(file))
    expect(stale).toEqual([])
  })
})
```

- [ ] **Step 3: Run it**

Run: `cd web && bun run test -- src/routes/-page-frame-adoption.test.ts`
Expected: PASS (the lists match reality). If `unexpected` is non-empty, add those paths to `PENDING` or `OUTSIDE_SHELL`.

- [ ] **Step 4: Commit**

```bash
git checkout -b feat/appshell-frame-2-adopt
git add web/src/routes/-page-frame-adoption.test.ts
# email scan, then:
git commit -m "test(web): guard against new hand-rolled page headings"
```

### Task 4: Adopt on analytics pages

**Files (modify):** `web/src/features/{events/EventsPage,sessions/SessionsPage,realtime/RealtimePage,journeys/JourneysPage,retention/RetentionPage,profiles/ProfilesPage,profiles/ProfileDetailPage,cohorts/CohortsPage,heatmaps/HeatmapsPage,vitals/VitalsPage,funnels/FunnelsPage,goals/GoalsPage,revenue/RevenueReport,replays/ReplaysPage,links/LinksPage,links/LinkStatsPage,links/PixelPage}.tsx` and `web/src/routes/app.$org.$site.revenue.tsx`. Use `width="wide"`.
**Test:** `-page-frame-adoption.test.ts` plus each page's existing tests.

**The transformation (apply to every file in this task and Tasks 5-7):**

Before, in any of these shapes:

```tsx
<div className="flex flex-col gap-5">
  <div className="flex items-center justify-between">
    <div>
      <h1 className="text-2xl font-semibold">Funnels</h1>
      <p className="text-sm text-muted-foreground">Track drop-off</p>
    </div>
    <Button>New funnel</Button>
  </div>
  ...rest
</div>
```

After:

```tsx
<PageContainer>
  <PageHeader title="Funnels" description="Track drop-off" actions={<Button>New funnel</Button>} />
  ...rest
</PageContainer>
```

Rules: `title` and `description` keep their existing expressions, whether they come from `en.*` or `ui.*`, so no new strings are introduced. Move the right-hand controls into `actions`. Delete the old heading wrapper and its Tailwind classes. Keep every non-heading child exactly as is. If a file has several return branches (loading, empty, error) that each render an `<h1>`, convert each branch to `PageHeader`, and drop any `sr-only` h1 workaround so every state has a visible `PageHeader`. Do not change data hooks, queries or props.

- [ ] **Step 1: Pick one file and run its tests as the baseline**

Run: `cd web && bun run test -- src/features/funnels 2>&1 | tail -10`
Expected: PASS (baseline).

- [ ] **Step 2: Remove the batch's entries from `PENDING`**

In `-page-frame-adoption.test.ts` delete the paths listed under Files. Run:

Run: `cd web && bun run test -- src/routes/-page-frame-adoption.test.ts`
Expected: FAIL with the batch's paths in `unexpected` (the test now demands their migration).

- [ ] **Step 3: Migrate each file with the transformation above**

Work file by file. After each file, run its feature tests (for example `bun run test -- src/features/funnels`). Expected: PASS. If a test asserted a removed wrapper class, update the assertion to the new structure (`getByRole('heading', { level: 1, name: ... })`).

- [ ] **Step 4: Run the guard, typecheck and the batch**

Run: `cd web && bun run typecheck && bun run test -- src/routes/-page-frame-adoption.test.ts src/features 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A web/src
# email scan, then:
git commit -m "refactor(web): adopt PageHeader on analytics pages"
```

### Task 5: Adopt on site settings pages

**Files (modify):** `web/src/features/settings/{SettingsPages,AlertsPage,ExclusionsPage,NotificationHistoryPage,ImportsExportPage}.tsx`, `web/src/features/integrations/IntegrationsPage.tsx`, `web/src/routes/app.$org.$site.settings*.tsx` where they render an `<h1>`. Use `width="narrow"`.

- [ ] **Step 1: Remove this batch's paths from `PENDING`; run the guard**

Run: `cd web && bun run test -- src/routes/-page-frame-adoption.test.ts`
Expected: FAIL listing exactly this batch's paths.

- [ ] **Step 2: Migrate each file** using the transformation in Task 4 with `<PageContainer width="narrow">`. Run each file's tests as you go (`bun run test -- src/features/settings`). Expected: PASS.

- [ ] **Step 3: Run the guard, typecheck and settings e2e**

Run: `cd web && bun run typecheck && bun run test -- src/routes/-page-frame-adoption.test.ts && bunx playwright test e2e/navigation.spec.ts 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add -A web/src
# email scan, then:
git commit -m "refactor(web): adopt PageHeader on site settings pages"
```

### Task 6: Adopt on organisation pages

**Files (modify):** `web/src/features/{health/HealthPage,sites/SitesPage,sites/SitesOverview,sites/Onboarding,api-docs/ApiDocsPage,settings/NotificationsPage}.tsx`, `web/src/routes/{app.$org.settings.index,app.$org.sites.new}.tsx`. `HealthPage`, `SitesPage`, `SitesOverview` use `width="wide"`; the rest `width="narrow"`. If `Onboarding` renders outside `AppShell` (check which route mounts it), move it to `OUTSIDE_SHELL` instead of migrating.

- [ ] **Step 1: Update `PENDING` and confirm the guard fails** as in Task 5 Step 1.
- [ ] **Step 2: Migrate each file**, running its tests as you go (`bun run test -- src/features/health src/features/sites src/routes/-org-settings-overview.test.tsx`). Expected: PASS.
- [ ] **Step 3: Run guard and typecheck:** `cd web && bun run typecheck && bun run test -- src/routes/-page-frame-adoption.test.ts && bunx playwright test e2e/sites-overview.spec.ts 2>&1 | tail -10`. Expected: PASS.
- [ ] **Step 4: Commit:** `git add -A web/src && git commit -m "refactor(web): adopt PageHeader on organisation pages"` (after the email scan).

### Task 7: Adopt on account and admin pages

**Files (modify):** `web/src/features/settings/{AccountProfilePage,AccountSessionsPage}.tsx`, `web/src/features/admin/AdminCommon.tsx`, `web/src/routes/{app.account.security,app.account.organizations.new}.tsx`. Use `width="narrow"`. These render inside `AccountFrame` today; if `AdminCommon` exports a shared heading helper used by several admin routes, replace its internals with `PageHeader` so every admin page migrates at once.

- [ ] **Step 1: Update `PENDING`; confirm the guard fails** (as Task 5 Step 1).
- [ ] **Step 2: Migrate each file;** run `bun run test -- src/features/admin src/features/settings` as you go. Expected: PASS.
- [ ] **Step 3: Verify the whole adoption:** `cd web && bun run typecheck && bun run test 2>&1 | tail -15 && bun run build 2>&1 | tail -8`. Expected: tests PASS, build succeeds, JS budget holds. `PENDING` must now be empty and the guard green.
- [ ] **Step 4: Run e2e groups** (small groups, login-heavy specs): `bunx playwright test e2e/account-navigation.spec.ts e2e/not-found.spec.ts 2>&1 | tail -10`, then `e2e/a11y-audit.spec.ts` and `e2e/ui-qa.spec.ts` separately. Expected: PASS, no new axe violations.
- [ ] **Step 5: Commit:** `git add -A web/src && git commit -m "refactor(web): adopt PageHeader on account and admin pages"` (after the email scan).

---

## Group 3: breadcrumbs (branch `feat/appshell-frame-3-breadcrumbs`)

### Task 8: Crumb model and breadcrumb primitive

**Files:**
- Create: `web/src/components/routeCrumbs.ts`, `web/src/components/routeCrumbs.test.ts`, `web/src/components/ui/breadcrumb.tsx` (via shadcn)
- Modify: `web/src/messages/ui.ts`

**Interfaces:**
- Consumes: `Membership` from `@/lib/me` (`membership.org.{name,slug}`, `membership.sites[].{slug,name}`).
- Produces:
  - `type Crumb = { label: string; to?: string; params?: Record<string, string> }`. The last crumb has no `to`.
  - `buildCrumbs(pathname: string, membership: Membership): Crumb[]`.

- [ ] **Step 1: Install the primitive**

Run: `cd web && bunx shadcn@latest add breadcrumb -y && git status --short`
Expected: `src/components/ui/breadcrumb.tsx` created. Open it and confirm it contains no unexpected network calls or scripts (it is a plain component file).

- [ ] **Step 2: Add the strings**

In `web/src/messages/ui.ts`, add inside the exported `ui` object (next to `appShell`):

```ts
  crumbs: {
    label: 'Breadcrumb',
    detail: 'Detail',
    account: 'Account',
    admin: 'Admin',
    segments: {
      sites: 'Sites', health: 'Health', settings: 'Settings', dashboard: 'Dashboard',
      events: 'Events', sessions: 'Sessions', realtime: 'Realtime', replays: 'Replays',
      journeys: 'Journeys', retention: 'Retention', profiles: 'Profiles', cohorts: 'Cohorts',
      heatmaps: 'Heatmaps', vitals: 'Web vitals', funnels: 'Funnels', goals: 'Goals',
      revenue: 'Revenue', links: 'Links', pixel: 'Pixel', general: 'General', alerts: 'Alerts',
      installation: 'Installation', integrations: 'Integrations', imports: 'Imports', reports: 'Reports',
      shares: 'Shares', tracking: 'Tracking', members: 'Members', invites: 'Invites', audit: 'Audit log',
      api: 'API', 'api-keys': 'API keys', notifications: 'Notifications', 'notification-history': 'Notification history',
      'replay-storage': 'Replay storage', new: 'New', profile: 'Profile', security: 'Security',
      organizations: 'Organizations', users: 'Users',
    } as Record<string, string>,
  },
```

Check the exact route file names in `web/src/routes/` against this map (`ls web/src/routes | grep -v test`) and add any segment that is missing. Do not guess.

- [ ] **Step 3: Write the failing tests**

Create `web/src/components/routeCrumbs.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import type { Membership } from '@/lib/me'

import { buildCrumbs } from './routeCrumbs'

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  sites: [{ id: 7, name: 'Docs', slug: 'docs' }],
}
const empty: Membership = { ...membership, sites: [] }
const labels = (path: string, m = membership) => buildCrumbs(path, m).map((c) => c.label)

describe('buildCrumbs', () => {
  it('dashboard: Org / Site / Dashboard', () => {
    expect(labels('/app/acme/docs')).toEqual(['Acme', 'Docs', 'Dashboard'])
  })
  it('section: Org / Site / Section', () => {
    expect(labels('/app/acme/docs/funnels')).toEqual(['Acme', 'Docs', 'Funnels'])
  })
  it('detail never shows the id', () => {
    expect(labels('/app/acme/docs/funnels/42')).toEqual(['Acme', 'Docs', 'Funnels', 'Detail'])
    expect(labels('/app/acme/docs/profiles/abc-9')).toEqual(['Acme', 'Docs', 'Profiles', 'Detail'])
    expect(labels('/app/acme/docs/links/9')).toEqual(['Acme', 'Docs', 'Links', 'Detail'])
  })
  it('site settings', () => {
    expect(labels('/app/acme/docs/settings/general')).toEqual(['Acme', 'Docs', 'Settings', 'General'])
  })
  it('org pages', () => {
    expect(labels('/app/acme/health')).toEqual(['Acme', 'Health'])
    expect(labels('/app/acme/sites')).toEqual(['Acme', 'Sites'])
    expect(labels('/app/acme/settings/members')).toEqual(['Acme', 'Settings', 'Members'])
  })
  it('account and admin have no org crumb', () => {
    expect(labels('/app/account/profile')).toEqual(['Account', 'Profile'])
    expect(labels('/app/admin/users')).toEqual(['Admin', 'Users'])
  })
  it('an org with no sites shows no site crumb', () => {
    expect(labels('/app/acme/sites', empty)).toEqual(['Acme', 'Sites'])
  })
  it('an unknown site slug falls back to the slug without throwing', () => {
    expect(labels('/app/acme/ghost/events')).toEqual(['Acme', 'ghost', 'Events'])
  })
  it('an unknown trailing segment is shown raw, never empty', () => {
    expect(labels('/app/acme/docs/zzz')).toEqual(['Acme', 'Docs', 'zzz'])
  })
  it('links every crumb except the last', () => {
    const crumbs = buildCrumbs('/app/acme/docs/funnels/42', membership)
    expect(crumbs.slice(0, -1).every((c) => c.to)).toBe(true)
    expect(crumbs.at(-1)?.to).toBeUndefined()
    expect(crumbs[0]).toMatchObject({ to: '/app/$org/sites', params: { org: 'acme' } })
    expect(crumbs[1]).toMatchObject({ to: '/app/$org/$site', params: { org: 'acme', site: 'docs' } })
  })
  it('ignores a trailing slash', () => {
    expect(labels('/app/acme/docs/funnels/')).toEqual(['Acme', 'Docs', 'Funnels'])
  })
})
```

- [ ] **Step 4: Run to verify failure**

Run: `cd web && bun run test -- src/components/routeCrumbs.test.ts`
Expected: FAIL, "Failed to resolve import './routeCrumbs'".

- [ ] **Step 5: Implement `buildCrumbs`**

Create `web/src/components/routeCrumbs.ts`:

```ts
import type { Membership } from '@/lib/me'
import { ui } from '@/messages/ui'

export type Crumb = { label: string; to?: string; params?: Record<string, string> }

const labelOf = (segment: string) => ui.crumbs.segments[segment]

/**
 * Org / Site / Section / Detail from a pathname. Pure: no router access, so it is
 * trivial to test. The last crumb is the current page and has no link.
 */
export function buildCrumbs(pathname: string, membership: Membership): Crumb[] {
  const parts = pathname.split('/').filter(Boolean) // ['app', ...]
  if (parts[0] !== 'app') return []
  const rest = parts.slice(1)
  const crumbs: Crumb[] = []

  if (rest[0] === 'account' || rest[0] === 'admin') {
    crumbs.push({ label: rest[0] === 'account' ? ui.crumbs.account : ui.crumbs.admin })
    pushTail(crumbs, rest.slice(1))
    return finish(crumbs)
  }

  const [org, second, ...after] = rest
  if (!org) return []
  crumbs.push({ label: membership.org.name, to: '/app/$org/sites', params: { org } })
  if (!second) return finish(crumbs)

  const isOrgPage = labelOf(second) !== undefined
  if (isOrgPage) {
    pushTail(crumbs, [second, ...after])
    return finish(crumbs)
  }

  const site = membership.sites.find((item) => item.slug === second)
  crumbs.push({ label: site?.name ?? second, to: '/app/$org/$site', params: { org, site: second } })
  pushTail(crumbs, after.length === 0 ? ['dashboard'] : after)
  return finish(crumbs)
}

function pushTail(crumbs: Crumb[], segments: string[]) {
  let previousKnown: string | undefined
  for (const segment of segments) {
    const known = labelOf(segment)
    if (known) {
      crumbs.push({ label: known })
      previousKnown = segment
    } else if (previousKnown && previousKnown !== 'settings') {
      crumbs.push({ label: ui.crumbs.detail })
    } else {
      crumbs.push({ label: segment })
    }
  }
}

// Only the last crumb is the page; clear any link on it.
function finish(crumbs: Crumb[]): Crumb[] {
  return crumbs.map((crumb, index) => (index === crumbs.length - 1 ? { label: crumb.label } : crumb))
}
```

- [ ] **Step 6: Run to verify pass**

Run: `cd web && bun run test -- src/components/routeCrumbs.test.ts && bun run typecheck`
Expected: PASS, 11 tests. The unknown-slug case passes because `labelOf('ghost')` is undefined, so it is treated as a site; "zzz" after a site becomes a raw segment because no known segment precedes it.

- [ ] **Step 7: Commit**

```bash
git checkout -b feat/appshell-frame-3-breadcrumbs
git add web/src/components/routeCrumbs.ts web/src/components/routeCrumbs.test.ts web/src/components/ui/breadcrumb.tsx web/src/messages/ui.ts web/package.json web/bun.lock
# email scan, then:
git commit -m "feat(web): derive breadcrumbs from the route"
```

### Task 9: Breadcrumbs component wired into the header

**Files:**
- Create: `web/src/components/Breadcrumbs.tsx`, `web/src/components/Breadcrumbs.test.tsx`
- Modify: `web/src/components/AppShell.tsx` (the header block, lines ~124-133), `web/src/components/AppShell.test.tsx`

**Interfaces:**
- Consumes: `buildCrumbs`, `Crumb` (Task 8); shadcn `Breadcrumb*` parts.
- Produces: `Breadcrumbs(props: { membership: Membership })`, which reads the pathname itself with `useRouterState({ select: (s) => s.location.pathname })`.

- [ ] **Step 1: Write the failing component test**

Create `web/src/components/Breadcrumbs.test.tsx`:

```tsx
import '@testing-library/jest-dom/vitest'

import { RouterProvider, createMemoryHistory, createRootRoute, createRouter } from '@tanstack/react-router'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import type { Membership } from '@/lib/me'

import { Breadcrumbs } from './Breadcrumbs'

afterEach(cleanup)

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  sites: [{ id: 7, name: 'Docs', slug: 'docs' }],
}

async function at(path: string) {
  const root = createRootRoute({ component: () => <Breadcrumbs membership={membership} /> })
  const router = createRouter({ routeTree: root, history: createMemoryHistory({ initialEntries: [path] }) })
  render(<RouterProvider router={router} />)
  await screen.findByRole('navigation', { name: 'Breadcrumb' })
}

describe('Breadcrumbs', () => {
  it('renders linked ancestors and a current-page last crumb', async () => {
    await at('/app/acme/docs/funnels')
    expect(screen.getByRole('link', { name: 'Acme' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Docs' })).toBeInTheDocument()
    expect(screen.getByText('Funnels')).toHaveAttribute('aria-current', 'page')
    expect(screen.queryByRole('link', { name: 'Funnels' })).toBeNull()
  })

  it('marks all but the last two crumbs as hidden on small screens', async () => {
    await at('/app/acme/docs/funnels/42')
    const items = screen.getByRole('navigation', { name: 'Breadcrumb' }).querySelectorAll('li[data-crumb]')
    expect([...items].map((li) => li.className.includes('hidden'))).toEqual([true, true, false, false])
  })

  it('renders nothing outside /app', async () => {
    const root = createRootRoute({ component: () => <Breadcrumbs membership={membership} /> })
    const router = createRouter({ routeTree: root, history: createMemoryHistory({ initialEntries: ['/login'] }) })
    const { container } = render(<RouterProvider router={router} />)
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(container.querySelector('nav')).toBeNull()
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && bun run test -- src/components/Breadcrumbs.test.tsx`
Expected: FAIL, "Failed to resolve import './Breadcrumbs'".

- [ ] **Step 3: Implement the component**

Create `web/src/components/Breadcrumbs.tsx`:

```tsx
import { Link, useRouterState, type LinkProps } from '@tanstack/react-router'
import { Fragment } from 'react'

import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@/components/ui/breadcrumb'
import type { Membership } from '@/lib/me'
import { ui } from '@/messages/ui'

import { buildCrumbs } from './routeCrumbs'

export function Breadcrumbs({ membership }: { membership: Membership }) {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const crumbs = buildCrumbs(pathname, membership)
  if (crumbs.length === 0) return null

  return (
    <Breadcrumb aria-label={ui.crumbs.label} className="min-w-0">
      <BreadcrumbList className="flex-nowrap">
        {crumbs.map((crumb, index) => {
          const last = index === crumbs.length - 1
          // Below md only the last two crumbs show, so the header never overflows.
          const hiddenOnMobile = index < crumbs.length - 2
          const visibility = hiddenOnMobile ? 'hidden md:inline-flex' : ''
          return (
            <Fragment key={`${index}-${crumb.label}`}>
              {index > 0 ? <BreadcrumbSeparator className={hiddenOnMobile ? 'hidden md:block' : ''} /> : null}
              <BreadcrumbItem className={`${visibility} min-w-0`} data-crumb>
                {last || !crumb.to ? (
                  <BreadcrumbPage className="truncate">{crumb.label}</BreadcrumbPage>
                ) : (
                  <BreadcrumbLink asChild>
                    {/* `to` is built from a fixed set of route templates in routeCrumbs.ts */}
                    <Link to={crumb.to as LinkProps['to']} params={crumb.params as never} className="truncate">
                      {crumb.label}
                    </Link>
                  </BreadcrumbLink>
                )}
              </BreadcrumbItem>
            </Fragment>
          )
        })}
      </BreadcrumbList>
    </Breadcrumb>
  )
}
```

If the installed `BreadcrumbItem` does not forward `data-crumb`, add `{...props}` forwarding in `ui/breadcrumb.tsx` (shadcn's version spreads props; confirm).

- [ ] **Step 4: Run to verify pass**

Run: `cd web && bun run test -- src/components/Breadcrumbs.test.tsx && bun run typecheck`
Expected: PASS, 3 tests.

- [ ] **Step 5: Write the failing AppShell assertion**

In `web/src/components/AppShell.test.tsx` add a test next to the existing header tests, using the file's existing `renderShell` helper and a site route entry such as `/app/acme/docs/settings/general`:

```tsx
  it('shows breadcrumbs in the header instead of a repeated site name', async () => {
    renderShell('/app/acme/docs/settings/general')
    const nav = await screen.findByRole('navigation', { name: 'Breadcrumb' })
    expect(within(nav).getByRole('link', { name: 'Acme' })).toBeInTheDocument()
    expect(within(nav).getByRole('link', { name: 'Docs' })).toBeInTheDocument()
    expect(within(nav).getByText('General')).toHaveAttribute('aria-current', 'page')
  })
```

Add `within` to the `@testing-library/react` import. Run it; it must FAIL (no breadcrumb nav yet).

Run: `cd web && bun run test -- src/components/AppShell.test.tsx -t "breadcrumbs"`
Expected: FAIL, nav not found.

- [ ] **Step 6: Wire it into the header**

In `web/src/components/AppShell.tsx` import `Breadcrumbs` and replace the block

```tsx
              {site ? (
                <div className="flex min-w-0 items-center gap-2">
                  <SiteIcon iconUrl={site.icon_url} name={site.name} size={24} />
                  <span className="hidden truncate text-sm font-medium sm:inline">{site.name}</span>
                </div>
              ) : null}
```

with

```tsx
              <Breadcrumbs membership={membership} />
```

If `SiteIcon` and `site` are now unused in `AppShell.tsx`, remove the unused import and variable only if typecheck reports them; `activeSite` is still used by the sidebar. Keep the header height and `ProjectTabs` as they are (step 4 of the spec removes them).

- [ ] **Step 7: Fix tests that asserted the old header site name**

Run: `cd web && bun run test -- src/components 2>&1 | tail -30`
Expected: failures only in tests that look for the site name text in the header. Update each to query the breadcrumb navigation. Do not weaken unrelated assertions.

- [ ] **Step 8: Full verification**

Run, and record the output:

```bash
cd web && bun run typecheck && bun run test 2>&1 | tail -15 && bun run build 2>&1 | tail -8
bunx playwright test e2e/navigation.spec.ts 2>&1 | tail -10
bunx playwright test e2e/a11y-audit.spec.ts 2>&1 | tail -10
bunx playwright test e2e/ui-qa.spec.ts 2>&1 | tail -10
```

Expected: all PASS, JS budget holds. Take screenshots of the dashboard, a funnel detail page and site settings at 1440, 1024, 768 and 390 px, open them at full size, and confirm: one breadcrumb row, no header overflow at 390 px, last two crumbs only on mobile.

- [ ] **Step 9: Commit**

```bash
git add -A web/src
# email scan, then:
git commit -m "feat(web): show breadcrumbs in the app header"
```

---

## Wrap-up

- [ ] Record the session in `progress.md` (what shipped, what is next) and decisions in `memory.md`. Per the repo rule, do this in the same PR as the last task or as a separate docs PR.
- [ ] Tick the spec's step list: steps 1-3 done; next plan covers steps 4-5 (header and switcher consolidation, sidebar rail), then step 6 plus the section 11 inspector.

## Self-review (spec coverage)

| Spec requirement | Task |
|---|---|
| Step 1 `PageContainer`, `PageHeader`, dashboard adoption, strings in `ui.ts` | 1, 2 |
| Step 2 adopt on all routes, no hand-rolled h1 left | 3-7 (guard test enforces) |
| Step 3 breadcrumb install, `routeCrumbs`, `Breadcrumbs`, header wiring, tests on site/org/settings routes | 8, 9 |
| Acceptance: breadcrumbs correct on 8 sampled routes | 8 covers dashboard, funnel detail, profile detail, site settings, org settings, health, account (link detail has its own test case) |
| Acceptance: no new hardcoded English | strings in `ui.crumbs`; adoption keeps existing expressions |
| Acceptance: 390 px no horizontal scroll | Task 9 Step 8 screenshots, `hidden md:` crumbs |
| Section 5.1 site name leaves the dashboard subtitle | Task 2 |
| Sections 5.2-5.4 (filter row, imported checkbox into filters), steps 4-8, section 11 | Deliberately out of this plan; separate plans |
