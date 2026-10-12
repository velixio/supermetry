# Account Shell and Command Palette Implementation Plan (steps 7-8)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every `/app/...` route, including account, admin, the sites overview, onboarding and an unknown organization, renders inside one `AppShell`, and that shell gets a lazily loaded command palette (Ctrl/⌘ K and a header trigger) whose entries mirror what the user can already reach.

**Architecture:** The `/app` route component becomes `AppLayout`: it reads the `org` URL param, finds the membership and renders `AppShell` once for the whole `/app` subtree. `AppShell` takes an optional membership. With one it is today's organization shell. Without one it shows a new `AccountSidebar` (an organization-only switcher, plus an "Account" group with Account settings and, for instance admins, Instance administration), breadcrumbs from `buildCrumbs` ("Account / Profile"), and no site context, live badge or privacy boundary. `AccountFrame` is deleted, and the account and admin layouts become plain content inside `PageContainer`. Step 8 adds `useShortcuts` support for `mod+` chords and a pure `paletteCommands()` that reuses the sidebar's own gating (`visibleNavigation`, `settingsDestinations`, `accountDestinations`, the replay rule, `organizationHref`). It also adds a `CommandPalette` (shadcn `command` on `cmdk@1.1.1`), loaded with `React.lazy` the first time it opens, and a header trigger.

**Tech Stack:** React 19, TypeScript, TanStack Router and Query, shadcn/ui (Radix dialog, sidebar, dropdown), `cmdk@1.1.1` (new), Tailwind v4, Vitest and Testing Library, Playwright (edited, not run), Bun.

**Spec:** `docs/superpowers/specs/2026-10-13-appshell-dashboard-design.md`, sections 1-3, 7, 8 (steps 7 and 8), 9 and 10. Palette notes: `docs/plans/2026-10-12-v2-app-shell-realtime.md` section 5.2. Steps 1-6 and section 11 are done on this branch (`PageContainer`, `PageHeader`, `Breadcrumbs`/`buildCrumbs`, `AppHeader`, the sidebar Settings group and `AccountMenu` footer, the icon rail, `defaultSearch()`, the dashboard filter row and inspector).

**Out of scope (v2 plan 5.2 items not built here):** date-range switching, density, "create goal", "copy tracking snippet", docs search and the `g o`/`g r`/`g s`/`[`/`]`/`/` keys. They need actions that do not exist yet, or they are separate features. The palette offers only navigation the user can already reach and three existing actions: toggle theme, show keyboard shortcuts and log out.

## Global Constraints

- Scope is `web/` only: no backend change and no new endpoint.
- Work from `/home/user/code/supermetry/.worktrees/shell45` (branch `feat/appshell-steps-4-8`). Run commands from `web/`. Do not create branches. Commit every task on `feat/appshell-steps-4-8`. Do not push.
- Use Bun only: `bun install --frozen-lockfile`, `bun add`, `bun run <script>`, `bunx`. Never use npm.
- The only new package is `cmdk`, pinned exactly to `1.1.1` (published 2025-03-14; its dependencies `@radix-ui/react-dialog ^1.1.6`, `react-id ^1.1.0`, `react-primitive ^2.0.2` and `react-compose-refs ^1.1.1` are already in `bun.lock` at 1.2.0, 1.1.4, 2.1.11 and 1.1.5). Do **not** run `shadcn add command`. A plan-time `--dry-run` showed it would overwrite `ui/button.tsx`, `ui/input.tsx`, `ui/textarea.tsx` and `ui/dialog.tsx`, and install a stray `cn` package. Task 7 writes `ui/command.tsx` by hand instead.
- JS budget: `bun run build` runs `web/scripts/size-check.mjs`, which fails above 256000 bytes gzip of initial JS. At plan time (commit `cb458734`) it was 220271. The palette must stay out of the initial JS: it loads through `React.lazy` on first open.
- No new hardcoded English. New strings go in `web/src/messages/en.ts` or `web/src/messages/ui.ts`, with the same keys in `web/src/messages/locales/{de,es}.ts` or `{de,es}.ui.ts`. `web/src/messages/locales/locales.test.ts` enforces parity. Read strings at render or call time, never at module scope. Existing hardcoded strings in files this plan touches stay as they are; do not translate them here.
- One shell: every `/app/...` route renders inside `AppShell`. It has exactly one `main` landmark (`id="main-content"`, `tabIndex={-1}`), and the skip link stays the first focusable element. Tab order is skip link, sidebar, header, main.
- One sticky bar: the 44 px `AppHeader` (`sticky top-0 h-11`). No other `h-14` header, and no `calc(100dvh-3.5rem)`.
- Privacy and gating: anonymous-tracking sites get no link (sidebar or palette) to a screen that `anonymousRouteRestricted()` blocks. Role and capability gating come from the existing `visibleNavigation`, `settingsDestinations` and `hasCapability` and are not re-implemented. `AnalyticsPrivacyBoundary` stays around the `Outlet` whenever there is a membership.
- The palette is the only global search (spec section 10). No search box or palette trigger appears before Task 8.
- No visible control without behaviour, and no fabricated entries or metrics.
- Use shadcn primitives, existing tokens and lucide icons at 16 px. Do not edit `web/src/components/ui/sidebar.tsx` or `ui/dialog.tsx`.
- Tests first. Playwright cannot run in the execution environment: the e2e webServer fails on DB login. Every behaviour is pinned by Vitest and Testing Library. The e2e edits listed in Tasks 4 and 8 must still be made. Mark them "edited, not run" in the commit body and the PR description, and never claim they pass.
- Commit identity is `velixio <270455167+velixio@users.noreply.github.com>`. Check it with `git config user.name` and `git config user.email`. Commit messages describe the change only: no `Co-Authored-By` or other trailers, and no tool or assistant names.
- Before every commit, scan the staged diff and the message for email addresses with `git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'`. Allowed: the velixio noreply address, and reserved test placeholders such as `owner@example.test` or `owner@supermetry.test` in test fixtures. Stop and ask the human about any other match.
- Baseline before Task 1 (run on 2026-10-13 at `cb458734`): `cd web && bun run test`. The result is 170 files: 169 passed, 1 failed. Tests: 1092 passed, 1 failed. The only failure is `packages/tokens/test/tokens.test.ts > generated token outputs > contain the current semantic values from tokens.json`. It is pre-existing and out of scope. "All tests pass" below means "everything except that one".

## Review Focus

- **A user with zero memberships.** On `/app` and `/app/account/*` the shell renders with the account sidebar. The switcher offers only "Add organization", there is no Settings group, and the palette has no site or organization pages. Pinned in Task 2 (switcher), Task 4 (`/app` no-org scope test) and Task 6 (`paletteCommands` without a membership).
- **An unknown organization slug (`/app/missing`) for a member of other organizations.** It renders inside the shell in account scope: a not-found page with its own h1, no breadcrumbs and one `main`. Pinned in Task 1 (`buildCrumbs` returns `[]`) and Task 3 (route test).
- **Anonymous-tracking site.** The palette offers no blocked screen and no Replays. Every palette `href` passes `anonymousRouteRestricted(href) === false`. Pinned in Task 6 (pure) and Task 8 (AppShell).
- **Viewer role, or no `profiles.read`/`replay.read` grant.** The palette has no Site settings, no Data health, no Profiles, Cohorts or Replays. It matches the sidebar exactly. Pinned in Task 6.
- **Moving between account and organization pages.** The shell stays mounted (same `sidebar-wrapper` element, sidebar cookie state kept), and sensitive cached queries are cleared because `authorizationScope` differs without a membership. Pinned in Task 1 (`authorizationScope`) and Task 3 (route test).

---

## File Structure

Line numbers refer to commit `cb458734`. Earlier tasks shift them, so find each spot by the quoted code.

Create:
- `web/src/components/AccountSidebar.tsx`: the sidebar outside an organization, with `accountDestinations(me)` and `accountDestinationIsActive(destination, pathname)`.
- `web/src/components/AccountSidebar.test.tsx`
- `web/src/components/AppHeader.test.tsx`
- `web/src/routes/-app-shell-scope.test.tsx`: full-route-tree tests for account, admin, no-org, onboarding, not-found, and shell persistence.
- `web/src/components/landmarks.test.ts`: a source guard that limits `<main` to the shell and the screens outside it, and bans the old 56 px header math.
- `web/src/components/paletteCommands.ts` and `paletteCommands.test.ts`: the pure palette entries.
- `web/src/components/ui/command.tsx`: the shadcn `command` primitive, written by hand on `cmdk`.
- `web/src/components/CommandPalette.tsx` and `CommandPalette.test.tsx`

Modify:
- `web/src/components/routeCrumbs.ts`, `Breadcrumbs.tsx`, `AppHeader.tsx`, `web/src/lib/authorization-cache.tsx`: accept a missing membership.
- `web/src/components/Sidebar.tsx`: extract `SidebarBrandLink`, `OrganizationMenuGroup` and `SidebarAccountFooter` for reuse.
- `web/src/components/AppShell.tsx`: optional membership; later the palette.
- `web/src/routes/app.tsx`: `AppLayout` renders the one shell.
- `web/src/components/OrganizationLayout.tsx`: `Outlet`, or a not-found page inside the shell.
- `web/src/routes/app.account.tsx`, `app.admin.tsx`, `app.index.tsx`, `web/src/features/sites/SitesOverviewLanding.tsx`, `Onboarding.tsx`, `web/src/features/lifecycle/NoOrgScreen.tsx`: content only, no frame and no `<main>`.
- `web/src/components/HeaderMenus.tsx`: keep only `AccountMenu` (the `HeaderMenus` component had one caller, `AccountFrame`).
- `web/src/lib/shortcuts.ts`: `mod+` chords and `isApplePlatform()`.
- `web/src/messages/ui.ts`, `en.ts`, `locales/{de,es}.ui.ts`, `locales/{de,es}.ts`: new strings.
- Tests: `AppShell.test.tsx`, `HeaderMenus.test.tsx`, `routeCrumbs.test.ts`, `sidebar-navigation.test.tsx`, `sticky-offsets.test.ts`, `web/src/lib/authorization-cache.test.tsx`, `web/src/lib/shortcuts.test.tsx`, `web/src/routes/-page-frame-adoption.test.ts`.
- `web/package.json`, `web/bun.lock`: `cmdk@1.1.1`.
- E2e (edit only, cannot run here): `web/e2e/account-navigation.spec.ts`, `navigation.spec.ts`, `a11y-audit.spec.ts`, `not-found.spec.ts`, `sites-overview.spec.ts`.
- Housekeeping in Task 1: `web/src/features/map/WhereCard.tsx` (stray blank line in the imports) and spec section 11.5 wording.

Delete:
- `web/src/components/AccountFrame.tsx`. Its header (sticky `h-14`, its own theme state and `HeaderMenus`) is replaced by `AppHeader` and the sidebar footer. Its `PoweredBySupermetry` footer is replaced by the shell footer (`GeolocationCredit`). `PoweredBySupermetry` stays in use by `AuthScreen` and `SharedDashboard`.

---

## Group A: AccountFrame onto the shell (spec step 7, Tasks 1-4)

### Task 1: Shell primitives that work without an organization (+ housekeeping)

**Files:**
- Modify: `web/src/components/routeCrumbs.ts:13,27`
- Modify: `web/src/components/Breadcrumbs.tsx:17`
- Modify: `web/src/components/AppHeader.tsx:53-85`
- Modify: `web/src/lib/authorization-cache.tsx:9-23`
- Modify: `web/src/features/map/WhereCard.tsx:16` (delete the blank line between the `./CountryTable` and `./trafficGrid` imports)
- Modify: `docs/superpowers/specs/2026-10-13-appshell-dashboard-design.md:185`
- Test: `web/src/components/routeCrumbs.test.ts`, `web/src/components/AppHeader.test.tsx` (new), `web/src/lib/authorization-cache.test.tsx`

**Interfaces:**
- Produces: `buildCrumbs(pathname: string, membership?: Membership): Crumb[]`; `Breadcrumbs({ membership }: { membership?: Membership })`; `AppHeader` prop `membership?: Membership`; `authorizationScope(userId: number, membership: Membership | undefined): string`.

- [ ] **Step 1: Write the failing tests**

Append to the `describe('buildCrumbs', ...)` block in `web/src/components/routeCrumbs.test.ts`, just before its closing `})`:

```ts
  it('without a membership keeps account and admin crumbs and shows none for an organization path', () => {
    expect(buildCrumbs('/app/account/security').map((c) => c.label)).toEqual(['Account', 'Security'])
    expect(buildCrumbs('/app/admin/sites').map((c) => c.label)).toEqual(['Admin', 'Sites'])
    expect(buildCrumbs('/app/missing')).toEqual([])
    expect(buildCrumbs('/app/missing/docs/events')).toEqual([])
    expect(buildCrumbs('/app')).toEqual([])
  })
```

Append to the last `describe` block in `web/src/lib/authorization-cache.test.tsx`, right after the test that ends with

```ts
    ])
      expect(next).not.toBe(original)
  })
```

the new test:

```ts

  it('gives pages outside an organization their own authorization context', () => {
    const membership = { org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner' as const, sites: [] }
    expect(authorizationScope(1, undefined)).toBe(authorizationScope(1, undefined))
    expect(authorizationScope(1, undefined)).not.toBe(authorizationScope(1, membership))
    expect(authorizationScope(1, undefined)).not.toBe(authorizationScope(2, undefined))
  })
```

Create `web/src/components/AppHeader.test.tsx`:

```tsx
import '@testing-library/jest-dom/vitest'
import { cleanup, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { useLiveSummary } = vi.hoisted(() => ({ useLiveSummary: vi.fn() }))
vi.mock('@/features/realtime/useLiveSummary', () => ({ useLiveSummary }))

import { SidebarProvider } from '@/components/ui/sidebar'
import { TooltipProvider } from '@/components/ui/tooltip'
import { renderWithRouter } from '@/test/renderWithRouter'

import { AppHeader } from './AppHeader'

beforeEach(() => {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
  })
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

function show(path: string) {
  renderWithRouter(
    <TooltipProvider>
      <SidebarProvider>
        <AppHeader theme="light" onToggleTheme={vi.fn()} onOpenHelp={vi.fn()} />
      </SidebarProvider>
    </TooltipProvider>,
    path,
  )
  return screen.findByRole('banner', { name: 'Application header' })
}

describe('AppHeader outside an organization', () => {
  it('shows account crumbs and the header actions, without a live badge', async () => {
    const header = await show('/app/account/profile')
    const crumbs = within(header).getByRole('navigation', { name: 'Breadcrumb' })
    expect(within(crumbs).getByRole('link', { name: 'Account' })).toHaveAttribute('href', '/app/account')
    expect(within(crumbs).getByText('Profile')).toHaveAttribute('aria-current', 'page')
    expect(within(header).getByRole('button', { name: 'Show keyboard shortcuts' })).toBeInTheDocument()
    expect(within(header).getByRole('button', { name: 'Use dark theme' })).toBeInTheDocument()
    expect(useLiveSummary).not.toHaveBeenCalled()
  })

  it('shows no breadcrumbs for an organization the user has no membership in', async () => {
    const header = await show('/app/missing')
    expect(within(header).queryByRole('navigation', { name: 'Breadcrumb' })).toBeNull()
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && bun run test src/components/routeCrumbs.test.ts src/components/AppHeader.test.tsx src/lib/authorization-cache.test.tsx`
Expected: FAIL. `buildCrumbs('/app/missing')` and the second AppHeader test throw `TypeError: Cannot read properties of undefined (reading 'org')`. `authorizationScope(1, undefined)` throws reading `org`.

- [ ] **Step 3: Implement**

`web/src/components/routeCrumbs.ts`: replace the doc comment and signature

```ts
/**
 * Org / Site / Section / Detail from a pathname. Pure: no router access, so it is
 * trivial to test. The last crumb is the current page and has no link.
 */
export function buildCrumbs(pathname: string, membership: Membership): Crumb[] {
```

with

```ts
/**
 * Org / Site / Section / Detail from a pathname. Pure: no router access, so it is
 * trivial to test. The last crumb is the current page and has no link.
 * Without a membership (sites overview, account, admin, an unknown organization)
 * only account and admin paths have crumbs.
 */
export function buildCrumbs(pathname: string, membership?: Membership): Crumb[] {
```

and replace `  if (!org) return []` with `  if (!org || !membership) return []`.

`web/src/components/Breadcrumbs.tsx`: replace `export function Breadcrumbs({ membership }: { membership: Membership }) {` with `export function Breadcrumbs({ membership }: { membership?: Membership }) {`.

`web/src/components/AppHeader.tsx`: replace the doc comment line

```tsx
/** The single 44px sticky bar above every page: sidebar trigger, breadcrumbs, live badge, help and theme. */
```

with

```tsx
/**
 * The single 44px sticky bar above every page: sidebar trigger, breadcrumbs, live badge, help and theme.
 * Outside an organization (no membership) there is no site, so no live badge.
 */
```

Replace `  membership: Membership` in the props type with `  membership?: Membership`. Replace `        {site && requestedSite ? (` with `        {membership && site && requestedSite ? (`.

`web/src/lib/authorization-cache.tsx`: replace

```ts
export function authorizationScope(
  userId: number,
  membership: Membership,
): string {
  return JSON.stringify([
```

with

```ts
export function authorizationScope(
  userId: number,
  membership: Membership | undefined,
): string {
  // Outside an organization (account, admin) no organization grant applies.
  if (!membership) return JSON.stringify([userId, null])
  return JSON.stringify([
```

`web/src/features/map/WhereCard.tsx`: delete the empty line between `import { CountryTable, type CountryMetricRow } from './CountryTable'` and `import type { TrafficMetric } from './trafficGrid'`.

`docs/superpowers/specs/2026-10-13-appshell-dashboard-design.md` line 185: replace

```text
- **6b. Wire the sources**: add Inspect to `BreakdownCard`, then Where, Weekly rhythm and the chart, one PR each if the diff grows.
```

with

```text
- **6b. Wire the sources**: add Inspect to `BreakdownCard`, then Where and Weekly rhythm, one PR each if the diff grows (chart points were dropped, see 11.7).
```

- [ ] **Step 4: Run the tests and the typecheck to verify they pass**

Run: `cd web && bun run test src/components/routeCrumbs.test.ts src/components/AppHeader.test.tsx src/lib/authorization-cache.test.tsx src/components/Breadcrumbs.test.tsx src/components/AppShell.test.tsx && bun run typecheck`
Expected: all PASS, and the typecheck exits 0.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/routeCrumbs.ts web/src/components/routeCrumbs.test.ts web/src/components/Breadcrumbs.tsx web/src/components/AppHeader.tsx web/src/components/AppHeader.test.tsx web/src/lib/authorization-cache.tsx web/src/lib/authorization-cache.test.tsx web/src/features/map/WhereCard.tsx docs/superpowers/specs/2026-10-13-appshell-dashboard-design.md
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'
git commit -m "refactor(web): let breadcrumbs, the header and the authorization scope work without an organization"
```

---

### Task 2: AccountSidebar for pages outside an organization

**Files:**
- Create: `web/src/components/AccountSidebar.tsx`
- Modify: `web/src/components/Sidebar.tsx` (header brand link `:251-254`, organisations section `:299-329`, footer `:417-433`, `useSidebar` destructure `:241`)
- Modify: `web/src/messages/ui.ts:77`, `web/src/messages/locales/de.ui.ts:78`, `web/src/messages/locales/es.ui.ts:78`
- Test: `web/src/components/AccountSidebar.test.tsx` (new), `web/src/components/sidebar-navigation.test.tsx`

**Interfaces:**
- Produces (in `Sidebar.tsx`): `SidebarBrandLink({ onClick }: { onClick: () => void })`, `OrganizationMenuGroup({ me, onSelect }: { me: Me; onSelect: () => void })` (a `DropdownMenuLabel` plus a `DropdownMenuGroup`; render it inside a `DropdownMenuContent`), and `SidebarAccountFooter({ me }: { me: Me })`.
- Produces (in `AccountSidebar.tsx`): `type AccountDestination = 'account' | 'admin'`, `accountDestinations(me: Me): AccountDestination[]`, `accountDestinationIsActive(destination: AccountDestination, pathname: string): boolean`, and `AccountSidebar({ me }: { me: Me })`.
- Produces: the string `ui.sidebar.chooseOrganization`.

- [ ] **Step 1: Write the failing tests**

In `web/src/components/sidebar-navigation.test.tsx`, add the import `import { accountDestinationIsActive, accountDestinations } from './AccountSidebar'` after the `./Sidebar` import. Then add before the final `})` of the `describe` block:

```ts

  it('offers account settings to everyone and instance administration only to instance admins', () => {
    const user = { id: 1, name: 'Owner', email: 'owner@example.test' }
    expect(accountDestinations({ user, memberships: [] })).toEqual(['account'])
    expect(accountDestinations({ user: { ...user, instance_admin: false }, memberships: [] })).toEqual(['account'])
    expect(accountDestinations({ user: { ...user, instance_admin: true }, memberships: [] })).toEqual(['account', 'admin'])
  })

  it('marks an account destination active only inside its own section', () => {
    for (const path of ['/app/account/profile', '/app/account/security', '/app/account/sessions']) {
      expect(accountDestinationIsActive('account', path)).toBe(true)
      expect(accountDestinationIsActive('admin', path)).toBe(false)
    }
    expect(accountDestinationIsActive('account', '/app/account/organizations/new')).toBe(false)
    expect(accountDestinationIsActive('account', '/app/accounting')).toBe(false)
    expect(accountDestinationIsActive('admin', '/app/admin/users')).toBe(true)
    expect(accountDestinationIsActive('admin', '/app/admin')).toBe(true)
    expect(accountDestinationIsActive('admin', '/app/administrator')).toBe(false)
    expect(accountDestinationIsActive('admin', '/app/acme/settings')).toBe(false)
  })
```

Create `web/src/components/AccountSidebar.test.tsx`:

```tsx
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { SidebarProvider } from '@/components/ui/sidebar'
import { TooltipProvider } from '@/components/ui/tooltip'
import type { Me } from '@/lib/me'
import { renderWithRouter } from '@/test/renderWithRouter'

import { AccountSidebar } from './AccountSidebar'

const me: Me = {
  user: { id: 1, name: 'Owner', email: 'owner@example.test' },
  memberships: [
    { org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner', sites: [{ id: 7, name: 'Docs', slug: 'docs' }] },
    { org: { id: 2, name: 'Other', slug: 'other' }, role: 'viewer', sites: [] },
  ],
}

beforeEach(() => {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
  })
  Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: 1440 })
})
afterEach(cleanup)

function show(path: string, user: Me = me) {
  renderWithRouter(
    <TooltipProvider>
      <SidebarProvider>
        <AccountSidebar me={user} />
      </SidebarProvider>
    </TooltipProvider>,
    path,
  )
  return screen.findByRole('complementary', { name: 'Application sidebar' })
}

function openSwitcher(sidebar: HTMLElement) {
  fireEvent.pointerDown(within(sidebar).getByRole('button', { name: 'Choose organization' }), { button: 0, ctrlKey: false, pointerType: 'mouse' })
  return screen.findByRole('menu')
}

describe('AccountSidebar', () => {
  it('links account settings, marks it current, and keeps the brand link and account menu', async () => {
    const sidebar = await show('/app/account/security')
    const nav = within(sidebar).getByRole('navigation', { name: 'Account' })
    expect(within(nav).getAllByRole('link').map((link) => link.textContent)).toEqual(['Account settings'])
    const link = within(nav).getByRole('link', { name: 'Account settings' })
    expect(link).toHaveAttribute('href', '/app/account/profile')
    expect(link).toHaveAttribute('aria-current', 'page')
    expect(within(sidebar).getByRole('link', { name: 'Supermetry home' })).toHaveAttribute('href', '/app')
    expect(within(sidebar).getByRole('button', { name: 'Account' })).toBeInTheDocument()
    expect(within(sidebar).queryByRole('navigation', { name: 'Settings' })).toBeNull()
  })

  it('adds instance administration for instance admins only', async () => {
    const sidebar = await show('/app/admin/sites', { ...me, user: { ...me.user, instance_admin: true } })
    const nav = within(sidebar).getByRole('navigation', { name: 'Account' })
    const admin = within(nav).getByRole('link', { name: 'Instance administration' })
    expect(admin).toHaveAttribute('href', '/app/admin/users')
    expect(admin).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'Account settings' })).not.toHaveAttribute('aria-current')
  })

  it('switches organizations without offering sites', async () => {
    const menu = await openSwitcher(await show('/app/account/profile'))
    expect(within(menu).getByRole('menuitem', { name: 'Acme' }).getAttribute('href')).toMatch(/^\/app\/acme\/docs\?/)
    expect(within(menu).getByRole('menuitem', { name: 'Other' })).toHaveAttribute('href', '/app/other/settings')
    expect(within(menu).getByRole('menuitem', { name: /Add organization/ })).toHaveAttribute('href', '/app/account/organizations/new')
    expect(within(menu).queryByRole('menuitem', { name: /Add site/ })).toBeNull()
    expect(within(menu).queryByText('Projects')).toBeNull()
  })

  it('offers only Add organization to a user without memberships', async () => {
    const menu = await openSwitcher(await show('/app/account/profile', { ...me, memberships: [] }))
    expect(within(menu).getAllByRole('menuitem').map((item) => item.textContent)).toEqual(['+ Add organization'])
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && bun run test src/components/AccountSidebar.test.tsx src/components/sidebar-navigation.test.tsx`
Expected: FAIL with `Failed to resolve import "./AccountSidebar"`.

- [ ] **Step 3: Add the string**

In `web/src/messages/ui.ts`, replace `  sidebar: { demo: 'Demo' },` with `  sidebar: { demo: 'Demo', chooseOrganization: 'Choose organization' },`.
In `web/src/messages/locales/de.ui.ts`, replace `  sidebar: { demo: 'Demo' },` with `  sidebar: { demo: 'Demo', chooseOrganization: 'Organisation wählen' },`.
In `web/src/messages/locales/es.ui.ts`, replace `  sidebar: { demo: 'Demo' },` with `  sidebar: { demo: 'Demo', chooseOrganization: 'Elegir organización' },`.

- [ ] **Step 4: Extract the shared sidebar pieces in `Sidebar.tsx`**

Add these three exported components directly above `export function SupermetrySidebar(`:

```tsx
/** The brand link at the top of every sidebar. It leads to the sites overview. */
export function SidebarBrandLink({ onClick }: { onClick: () => void }) {
  return (
    <Link aria-label="Supermetry home" className="mb-1 flex h-8 items-center gap-2 px-2 group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0" to="/app" onClick={onClick}>
      <BrandLogo className="h-7 w-7" decorative mark />
      <span className="truncate text-base font-semibold group-data-[collapsible=icon]:hidden">{en.navigation.brand}</span>
    </Link>
  )
}

/** The organisations part of the switcher: every membership, then "Add organization". */
export function OrganizationMenuGroup({ me, onSelect }: { me: Me; onSelect: () => void }) {
  return (
    <>
      <DropdownMenuLabel>{en.navigation.organizations}</DropdownMenuLabel>
      <DropdownMenuGroup>
        {me.memberships.map((item) => (
          <DropdownMenuItem key={item.org.id} asChild>
            {item.sites[0] ? (
              <Link
                to="/app/$org/$site"
                params={{ org: item.org.slug, site: siteSlug(item.sites[0]) }}
                search={defaultSearch()}
                onClick={onSelect}
              >
                {item.org.name}
              </Link>
            ) : (
              <Link
                to="/app/$org/settings"
                params={{ org: item.org.slug }}
                onClick={onSelect}
              >
                {item.org.name}
              </Link>
            )}
          </DropdownMenuItem>
        ))}
        <DropdownMenuItem asChild>
          <Link to="/app/account/organizations/new" onClick={onSelect}>
            <PlusIcon />
            {en.navigation.addOrganization}
          </Link>
        </DropdownMenuItem>
      </DropdownMenuGroup>
    </>
  )
}

/** The account menu in the footer of both sidebars. */
export function SidebarAccountFooter({ me }: { me: Me }) {
  const { isMobile } = useSidebar()
  return (
    <SidebarFooter>
      <SidebarMenu>
        <SidebarMenuItem>
          <AccountMenu
            align="end"
            me={me}
            side={isMobile ? 'top' : 'right'}
            trigger={
              <SidebarMenuButton className="h-11" tooltip={ui.menus.account}>
                <UserRoundIcon aria-hidden="true" />
                <span>{ui.menus.account}</span>
              </SidebarMenuButton>
            }
          />
        </SidebarMenuItem>
      </SidebarMenu>
    </SidebarFooter>
  )
}
```

Then, in `SupermetrySidebar`:
1. Replace `  const { setOpenMobile, isMobile } = useSidebar()` with `  const { setOpenMobile } = useSidebar()`.
2. Replace the brand `<Link aria-label="Supermetry home" ...>...</Link>` block (4 lines, starting `        <Link aria-label="Supermetry home"`) with `        <SidebarBrandLink onClick={closeMobile} />`.
3. Replace everything from `            <DropdownMenuLabel>{en.navigation.organizations}</DropdownMenuLabel>` through the `</DropdownMenuGroup>` that closes the organisations group (the one right before `          </DropdownMenuContent>`) with `            <OrganizationMenuGroup me={me} onSelect={closeMobile} />`. Keep the `<DropdownMenuSeparator />` above it.
4. Replace the whole `<SidebarFooter>...</SidebarFooter>` block with `      <SidebarAccountFooter me={me} />`.

- [ ] **Step 5: Create `web/src/components/AccountSidebar.tsx`**

```tsx
import { Link, useRouterState } from '@tanstack/react-router'
import { Building2Icon, ShieldCheckIcon, UserRoundIcon } from 'lucide-react'

import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from '@/components/ui/sidebar'
import type { Me } from '@/lib/me'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'

import { OrganizationMenuGroup, SidebarAccountFooter, SidebarBrandLink } from './Sidebar'

export type AccountDestination = 'account' | 'admin'

/** Account settings for everyone; instance administration only for instance admins. */
export function accountDestinations(me: Me): AccountDestination[] {
  return me.user.instance_admin ? ['account', 'admin'] : ['account']
}

const accountSections = ['/app/account/profile', '/app/account/security', '/app/account/sessions']

export function accountDestinationIsActive(destination: AccountDestination, pathname: string): boolean {
  const within = (base: string) => pathname === base || pathname.startsWith(`${base}/`)
  return destination === 'admin' ? within('/app/admin') : accountSections.some(within)
}

/**
 * The sidebar outside an organization: the sites overview, account and admin pages,
 * and an organization the user cannot open. It has no site switcher and no analytics navigation.
 */
export function AccountSidebar({ me }: { me: Me }) {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const { setOpenMobile } = useSidebar()
  const closeMobile = () => setOpenMobile(false)

  return (
    <Sidebar aria-label={en.a11y2.applicationSidebar} collapsible="icon" role="complementary">
      <SidebarHeader>
        <SidebarBrandLink onClick={closeMobile} />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton className="h-auto py-1.5 group-data-[collapsible=icon]:p-1.5!" tooltip={ui.sidebar.chooseOrganization}>
              <Building2Icon aria-hidden="true" />
              <span className="truncate">{ui.sidebar.chooseOrganization}</span>
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="min-w-56">
            <OrganizationMenuGroup me={me} onSelect={closeMobile} />
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarHeader>
      <SidebarContent className="group-data-[collapsible=icon]:overflow-auto">
        <SidebarGroup>
          <SidebarGroupLabel asChild><h2 id="sidebar-account-heading">{ui.menus.account}</h2></SidebarGroupLabel>
          <SidebarGroupContent>
            <nav aria-labelledby="sidebar-account-heading">
              <SidebarMenu>
                {accountDestinations(me).map((destination) => {
                  const active = accountDestinationIsActive(destination, pathname)
                  const label = destination === 'admin' ? en.admin.link : ui.menus.accountSettings
                  return (
                    <SidebarMenuItem key={destination}>
                      <SidebarMenuButton asChild className="h-11" isActive={active} tooltip={label}>
                        <Link
                          aria-current={active ? 'page' : undefined}
                          onClick={closeMobile}
                          to={destination === 'admin' ? '/app/admin/users' : '/app/account/profile'}
                        >
                          {destination === 'admin' ? <ShieldCheckIcon /> : <UserRoundIcon />}
                          <span>{label}</span>
                        </Link>
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  )
                })}
              </SidebarMenu>
            </nav>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarAccountFooter me={me} />
    </Sidebar>
  )
}
```

- [ ] **Step 6: Run the tests to verify they pass, including the existing sidebar suites**

Run: `cd web && bun run test src/components/AccountSidebar.test.tsx src/components/sidebar-navigation.test.tsx src/components/spa-navigation.test.tsx src/components/AppShell.test.tsx src/messages/locales/locales.test.ts && bun run typecheck`
Expected: all PASS. `spa-navigation` and `AppShell` show the extraction did not change `SupermetrySidebar`.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/AccountSidebar.tsx web/src/components/AccountSidebar.test.tsx web/src/components/Sidebar.tsx web/src/components/sidebar-navigation.test.tsx web/src/messages/ui.ts web/src/messages/locales/de.ui.ts web/src/messages/locales/es.ui.ts
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'
git commit -m "feat(web): add the account sidebar for pages outside an organization"
```

---

### Task 3: One shell for every /app route

**Files:**
- Modify: `web/src/routes/app.tsx` (whole file)
- Modify: `web/src/components/AppShell.tsx` (whole file)
- Modify: `web/src/components/OrganizationLayout.tsx` (whole file)
- Modify: `web/src/routes/app.account.tsx:1-28`, `web/src/routes/app.admin.tsx:1-38`, `web/src/routes/app.index.tsx:5,42-56`, `web/src/features/sites/SitesOverviewLanding.tsx:4,25-41`
- Modify: `web/src/components/HeaderMenus.tsx` (drop the `HeaderMenus` component)
- Delete: `web/src/components/AccountFrame.tsx`
- Test: `web/src/components/AppShell.test.tsx`, `web/src/routes/-app-shell-scope.test.tsx` (new), `web/src/components/HeaderMenus.test.tsx` (rewrite), `web/src/components/sticky-offsets.test.ts`, `web/src/routes/-page-frame-adoption.test.ts`

**Interfaces:**
- Consumes: `AccountSidebar({ me })` (Task 2), `authorizationScope(userId, membership | undefined)`, `AppHeader` with an optional `membership`, `Breadcrumbs` with an optional `membership` (Task 1).
- Produces: `AppShell({ me, membership }: { me: Me; membership?: Membership })`, which the `/app` route renders once. `OrganizationLayout` renders `<Outlet />` or a not-found page. `HeaderMenus.tsx` exports only `AccountMenu`.

- [ ] **Step 1: Write the failing tests**

In `web/src/components/AppShell.test.tsx`, add this helper below `function setWidth(...)`:

```tsx
// The shell outside an organization: the /app route renders AppShell without a membership.
function renderAppShell(initialEntry: string, shellMe: Me = me) {
  const root = createRootRoute({ component: Outlet })
  const app = createRoute({ getParentRoute: () => root, path: 'app', component: () => <AppShell me={shellMe} /> })
  const account = createRoute({ getParentRoute: () => app, path: 'account', component: Outlet })
  const profile = createRoute({ getParentRoute: () => account, path: 'profile', component: () => <h1>Profile</h1> })
  const admin = createRoute({ getParentRoute: () => app, path: 'admin', component: Outlet })
  const users = createRoute({ getParentRoute: () => admin, path: 'users', component: () => <h1>Users</h1> })
  const router = createRouter({
    routeTree: root.addChildren([app.addChildren([account.addChildren([profile]), admin.addChildren([users])])]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { router, ...view }
}
```

Add these tests at the end of the `describe('AppShell live visitor badge', ...)` block, before its closing `})`:

```tsx
  it('renders account pages in the same shell with account links and no site switcher', async () => {
    const { container } = renderAppShell('/app/account/profile')
    const header = await screen.findByRole('banner', { name: 'Application header' })
    expect(header).toHaveClass('sticky', 'top-0', 'h-11')
    expect(container.querySelectorAll('main')).toHaveLength(1)
    expect(screen.getByRole('main', { name: 'Main content' })).toHaveAttribute('id', 'main-content')
    expect(screen.getByRole('heading', { name: 'Profile' })).toBeVisible()
    const crumbs = within(header).getByRole('navigation', { name: 'Breadcrumb' })
    expect(within(crumbs).getByRole('link', { name: 'Account' })).toHaveAttribute('href', '/app/account')
    expect(within(crumbs).getByText('Profile')).toHaveAttribute('aria-current', 'page')
    const sidebar = screen.getByRole('complementary', { name: 'Application sidebar' })
    const account = within(sidebar).getByRole('navigation', { name: 'Account' })
    expect(within(account).getByRole('link', { name: 'Account settings' })).toHaveAttribute('aria-current', 'page')
    expect(within(account).queryByRole('link', { name: 'Instance administration' })).toBeNull()
    expect(within(sidebar).queryByRole('navigation', { name: 'Settings' })).toBeNull()
    expect(within(sidebar).queryByRole('button', { name: /Site/ })).toBeNull()
    expect(useLiveSummary).not.toHaveBeenCalled()
    expect(privateRequest).not.toHaveBeenCalled()
  })

  it('keeps the skip link first and focus order intact outside an organization', async () => {
    const { container } = renderAppShell('/app/admin/users', { ...me, user: { ...me.user, instance_admin: true } })
    const skip = await screen.findByRole('link', { name: 'Skip to main content' })
    expect(container.querySelector('a,button,input,select,textarea,[tabindex]:not([tabindex="-1"])')).toBe(skip)
    const sidebar = screen.getByRole('complementary', { name: 'Application sidebar' })
    const follows = (a: Node, b: Node) => Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)
    expect(follows(skip, sidebar)).toBe(true)
    expect(follows(sidebar, screen.getByRole('banner', { name: 'Application header' }))).toBe(true)
    expect(within(sidebar).getByRole('link', { name: 'Instance administration' })).toHaveAttribute('aria-current', 'page')
  })
```

Create `web/src/routes/-app-shell-scope.test.tsx`:

```tsx
import '@testing-library/jest-dom/vitest'

import { QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Me, Membership } from '@/lib/me'
import { queryClient } from '@/lib/queryClient'
import { routeTree } from '@/routeTree.gen'

const acme: Membership = { org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner', sites: [{ id: 7, name: 'Docs', public_id: 'docs-public' }] }
const me: Me = { user: { id: 1, name: 'Owner', email: 'owner@example.test' }, memberships: [acme] }

// /api/private/me answers with the given user; deleted organizations are an empty list; anything else an empty page.
function routedFetch(body: Me) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const path = new URL(url, 'http://localhost').pathname
    const json = path === '/api/private/me' ? body : path === '/api/private/me/orgs/deleted' ? [] : { data: [] }
    return new Response(JSON.stringify(json), { status: 200, headers: { 'Content-Type': 'application/json' } })
  })
}

function renderRoute(path: string, body: Me = me) {
  vi.stubGlobal('fetch', routedFetch(body))
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [path] }) })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return router
}

beforeEach(() => {
  queryClient.clear()
  window.scrollTo = vi.fn()
  Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: 1440 })
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
  })
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  })
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  vi.unstubAllGlobals()
})

// The one shell: a 44px sticky header, the sidebar, and exactly one main landmark.
async function expectShell() {
  const header = await screen.findByRole('banner', { name: 'Application header' })
  expect(header).toHaveClass('sticky', 'top-0', 'h-11')
  expect(document.querySelectorAll('main')).toHaveLength(1)
  expect(screen.getByRole('main', { name: 'Main content' })).toHaveAttribute('id', 'main-content')
  return { header, sidebar: screen.getByRole('complementary', { name: 'Application sidebar' }) }
}

describe('one shell for every /app route', () => {
  it('renders account pages inside the shell with account breadcrumbs and links', async () => {
    renderRoute('/app/account/security')
    expect(await screen.findByRole('heading', { name: 'Account security' })).toBeVisible()
    const { header, sidebar } = await expectShell()
    const crumbs = within(header).getByRole('navigation', { name: 'Breadcrumb' })
    expect(within(crumbs).getByRole('link', { name: 'Account' })).toHaveAttribute('href', '/app/account')
    expect(within(crumbs).getByText('Security')).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('navigation', { name: 'Account settings' })).toBeVisible()
    expect(within(sidebar).getByRole('link', { name: 'Account settings' })).toHaveAttribute('aria-current', 'page')
    expect(within(sidebar).queryByRole('navigation', { name: 'Settings' })).toBeNull()
  })

  it('renders instance administration inside the shell for an instance admin', async () => {
    renderRoute('/app/admin/users', { ...me, user: { ...me.user, instance_admin: true } })
    expect(await screen.findByRole('navigation', { name: 'Instance administration sections' })).toBeVisible()
    const { sidebar } = await expectShell()
    expect(within(sidebar).getByRole('link', { name: 'Instance administration' })).toHaveAttribute('aria-current', 'page')
  })

  it('keeps the same shell mounted from account pages into an organization', async () => {
    const router = renderRoute('/app/account/security')
    await screen.findByRole('heading', { name: 'Account security' })
    const wrapper = document.querySelector('[data-slot="sidebar-wrapper"]')
    expect(wrapper).not.toBeNull()
    await act(async () => {
      await router.navigate({ to: '/app/$org', params: { org: 'acme' } })
    })
    expect(await screen.findByRole('navigation', { name: 'Settings' })).toBeInTheDocument()
    expect(document.querySelector('[data-slot="sidebar-wrapper"]')).toBe(wrapper)
    await expectShell()
  })

  it('renders an unknown organization as a page inside the shell', async () => {
    renderRoute('/app/missing')
    expect(await screen.findByText('We could not find that organisation')).toBeVisible()
    const { header } = await expectShell()
    expect(screen.getByRole('heading', { level: 1, name: 'Not found' })).toBeVisible()
    expect(within(header).queryByRole('navigation', { name: 'Breadcrumb' })).toBeNull()
    expect(screen.getByRole('link', { name: 'Acme' })).toHaveAttribute('href', '/app/acme/docs-public')
  })
})
```

Replace `web/src/components/HeaderMenus.test.tsx` with this content. The `HeaderMenus` component and `AccountFrame` are deleted. Their settings-link coverage lives in the sidebar Settings group tests (`sidebar-navigation.test.tsx` and the `AppShell.test.tsx` alias and role cases).

```tsx
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { AnchorHTMLAttributes } from 'react'

import { AccountMenu } from './HeaderMenus'
import { requestLogout } from '@/lib/session-lifecycle'
import type { Me } from '@/lib/me'

vi.mock('@/lib/session-lifecycle', () => ({ requestLogout: vi.fn().mockResolvedValue(undefined) }))
vi.mock('@tanstack/react-router', async (importOriginal) => ({
  ...await importOriginal<object>(),
  Link: ({ to, params, ...props }: AnchorHTMLAttributes<HTMLAnchorElement> & { to: string; params?: Record<string, string> }) => (
    <a {...props} href={to.replace(/\$(\w+)/g, (_, key: string) => params?.[key] ?? key)} />
  ),
}))

const me: Me = {
  user: { id: 1, name: 'Alex Example', email: 'alex@example.test' },
  memberships: [{ org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner', sites: [] }],
}
function openMenu(name: string) {
  fireEvent.keyDown(screen.getByRole('button', { name }), { key: 'ArrowDown' })
}
afterEach(() => { cleanup(); vi.clearAllMocks() })

it.each([true, false])('shows identity and explicit current-session logout with organization access=%s', async (hasOrganization) => {
  render(<AccountMenu me={{ ...me, memberships: hasOrganization ? me.memberships : [] }} />)
  openMenu('Account')
  expect(await screen.findByText('Alex Example')).toBeVisible()
  expect(screen.getByText('alex@example.test')).toBeVisible()
  expect(screen.getByText('Supermetry dev')).toBeVisible()
  expect(screen.getByRole('menuitem', { name: 'Account settings' })).toHaveAttribute('href', '/app/account/profile')
  expect(screen.queryByRole('menuitem', { name: 'Instance administration' })).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('menuitem', { name: 'Log out' }))
  expect(requestLogout).toHaveBeenCalledTimes(1)
})

it('shows the server build version in the account menu', async () => {
  render(<AccountMenu me={{ ...me, version: '1.4.2' }} />)
  openMenu('Account')
  expect(await screen.findByText('Supermetry 1.4.2')).toBeVisible()
})

it('opens the same account menu from a caller-supplied trigger', async () => {
  render(
    <AccountMenu
      me={{ ...me, user: { ...me.user, instance_admin: true } }}
      side="right"
      trigger={<button type="button">Account</button>}
    />,
  )
  expect(screen.getAllByRole('button', { name: 'Account' })).toHaveLength(1)
  openMenu('Account')
  expect(await screen.findByRole('menu', { name: 'Account menu' })).toBeVisible()
  expect(screen.getByText('Alex Example')).toBeVisible()
  expect(screen.getByRole('menuitem', { name: 'Account settings' })).toHaveAttribute('href', '/app/account/profile')
  expect(screen.getByRole('menuitem', { name: 'Instance administration' })).toHaveAttribute('href', '/app/admin/users')
  fireEvent.click(screen.getByRole('menuitem', { name: 'Log out' }))
  expect(requestLogout).toHaveBeenCalledTimes(1)
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && bun run test src/components/AppShell.test.tsx src/routes/-app-shell-scope.test.tsx`
Expected: FAIL. The new AppShell tests throw `TypeError: Cannot read properties of undefined (reading 'sites')`. The scope tests find no banner named "Application header" on account, admin or not-found routes (`AccountFrame` renders an unnamed `h-14` header), and the not-found route renders two `main` elements.

- [ ] **Step 3: Make `AppShell` take an optional membership**

Replace `web/src/components/AppShell.tsx` with:

```tsx
import { AuthorizationCacheBoundary, authorizationScope } from '@/lib/authorization-cache'
import { AuthorizationProvider } from '@/lib/authorization'
import { useEffect, useRef, useState } from 'react'
import { Outlet, useRouterState } from '@tanstack/react-router'

import { AccountSidebar } from './AccountSidebar'
import { AnalyticsPrivacyBoundary } from './AnalyticsPrivacyBoundary'
import { AppHeader } from './AppHeader'
import { GeolocationCredit } from '@/components/GeolocationCredit'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import { Toaster } from '@/components/ui/sonner'
import { TooltipProvider } from '@/components/ui/tooltip'
import { useShortcuts } from '@/lib/shortcuts'
import { useTheme } from '@/lib/theme'
import { en } from '@/messages/en'
import type { Me, Membership } from '@/lib/me'

import { matchesSite, SupermetrySidebar } from './Sidebar'
import { DemoBanner } from '@/features/demo/DemoBanner'
import { RemoveDemoBanner } from '@/features/demo/RemoveDemoBanner'
import { privateRequest } from '@/features/settings/privateApi'
import { sidebarStartsOpen } from './sidebarState'

/**
 * The one shell around every /app route. With a membership it is the organization shell: site switcher,
 * analytics navigation, live badge and privacy boundary. Without one (sites overview, account, admin,
 * an organization the user cannot open) it shows the account sidebar and no site context.
 */
export function AppShell({
  me,
  membership,
}: {
  me: Me
  membership?: Membership
}) {
  const { theme, toggle } = useTheme()
  const [helpOpen, setHelpOpen] = useState(false)
  const [sidebarOpen] = useState(() => sidebarStartsOpen(document.cookie, window.innerWidth))
  const { pathname, requestedSite, renderedPathnames } = useRouterState({
    select: (state) => {
      const params = state.matches.at(-1)?.params
      return {
        pathname: state.location.pathname,
        requestedSite: params && 'site' in params ? params.site : undefined,
        // Location advances before Outlet commits its active matches. Cover
        // both snapshots so back/forward cannot briefly mount a blocked page.
        renderedPathnames: [state.resolvedLocation?.pathname ?? '', ...state.matches.map((match) => match.routeId)],
      }
    },
  })
  const scopedSite = membership?.sites.find((item) => matchesSite(item, requestedSite))
  const recordedLastSite = useRef(me.preferences?.last_site_id)

  useEffect(() => {
    if (!scopedSite || recordedLastSite.current === scopedSite.id) return
    recordedLastSite.current = scopedSite.id
    void privateRequest('PATCH', '/api/private/me', { body: { preferences: { last_site_id: scopedSite.id } } }).catch(() => undefined)
  }, [scopedSite])

  useShortcuts({
    d: toggle,
    '?': () => setHelpOpen(true),
  })

  return (
    <AuthorizationProvider membership={membership}>
    <TooltipProvider>
      <SidebarProvider defaultOpen={sidebarOpen}>
        <a
          className="absolute -left-[10000px] top-auto h-px w-px overflow-hidden whitespace-nowrap focus:fixed focus:left-4 focus:top-4 focus:z-[100] focus:h-auto focus:w-auto focus:overflow-visible focus:rounded-full focus:bg-background focus:px-4 focus:py-2 focus:text-sm focus:font-medium focus:text-foreground focus:shadow-lg focus:ring-2 focus:ring-ring focus:ring-offset-2 focus:ring-offset-background"
          href="#main-content"
          onClick={(event) => {
            event.preventDefault()
            document.getElementById('main-content')?.focus()
          }}
        >
          {en.a11y2.skipToMain}
        </a>
        {membership ? (
          <SupermetrySidebar me={me} membership={membership} requestedSite={requestedSite} />
        ) : (
          <AccountSidebar me={me} />
        )}
        <SidebarInset>
          <AppHeader
            membership={membership}
            site={scopedSite}
            requestedSite={requestedSite}
            theme={theme}
            onToggleTheme={toggle}
            onOpenHelp={() => setHelpOpen(true)}
          />
          <main aria-label={en.a11y2.mainContent} className="min-w-0 flex-1 p-4 md:p-6" id="main-content" tabIndex={-1}>
            {membership ? (
              scopedSite?.is_demo ? (
                <DemoBanner org={membership.org} site={scopedSite} />
              ) : (
                <RemoveDemoBanner currentSite={scopedSite} membership={membership} />
              )
            ) : null}
            <AuthorizationCacheBoundary scope={authorizationScope(me.user.id, membership)}>
              <div
                key={pathname}
                className="motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-1 motion-safe:duration-150"
              >
                {membership ? (
                  <AnalyticsPrivacyBoundary site={scopedSite} org={membership.org.slug} requestedSite={requestedSite} pathname={pathname} renderedPathnames={renderedPathnames}>
                    <Outlet />
                  </AnalyticsPrivacyBoundary>
                ) : (
                  <Outlet />
                )}
              </div>
            </AuthorizationCacheBoundary>
          </main>
          <footer className="px-4 pb-4 text-center text-xs text-muted-foreground md:px-6">
            <GeolocationCredit />
          </footer>
        </SidebarInset>
      </SidebarProvider>
      <Dialog open={helpOpen} onOpenChange={setHelpOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{en.shortcuts.title}</DialogTitle>
            <DialogDescription>{en.shortcuts.description}</DialogDescription>
          </DialogHeader>
          <ul className="flex flex-col gap-2 text-sm">
            <li>{en.shortcuts.theme}</li>
            <li>{en.shortcuts.help}</li>
            <li>{en.shortcuts.sidebar}</li>
            <li>{en.shortcuts.period}</li>
            <li>{en.shortcuts.compare}</li>
          </ul>
        </DialogContent>
      </Dialog>
      <Toaster />
    </TooltipProvider>
    </AuthorizationProvider>
  )
}
```

- [ ] **Step 4: Mount the shell once on `/app`**

Replace `web/src/routes/app.tsx` with:

```tsx
import { createFileRoute, redirect, useRouterState } from '@tanstack/react-router'

import { AppShell } from '@/components/AppShell'
import { AuthenticationRequired, meQueryOptions, useMe } from '@/lib/me'
import { queryClient } from '@/lib/queryClient'
import { en } from '@/messages/en'

export const Route = createFileRoute('/app')({
  beforeLoad: async ({ location }) => {
    try { await queryClient.ensureQueryData(meQueryOptions) }
    catch (error) {
      if (error instanceof AuthenticationRequired) throw redirect({ to: '/login', search: { next: location.href } })
      throw error
    }
  },
  component: AppLayout,
})

/**
 * Every /app route renders inside this one shell, so moving between account, admin and
 * organization pages never remounts it. The organization comes from the URL; the sites
 * overview, account and admin pages have none.
 */
function AppLayout() {
  const me = useMe()
  const org = useRouterState({
    select: (state) => {
      const params = state.matches.at(-1)?.params
      return params && 'org' in params ? params.org : undefined
    },
  })
  if (!me.data) {
    // Only before the shell exists; beforeLoad has normally filled the cache already.
    return (
      <main className="grid min-h-dvh place-items-center px-6">
        <p className="text-sm text-muted-foreground" role="status">{en.loadingDashboard}</p>
      </main>
    )
  }
  const membership = org === undefined ? undefined : me.data.memberships.find((item) => item.org.slug === org)
  return <AppShell me={me.data} membership={membership} />
}
```

Replace `web/src/components/OrganizationLayout.tsx` with:

```tsx
import { Link, Outlet } from '@tanstack/react-router'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { organizationHref } from '@/components/Sidebar'
import { useMe, type Me } from '@/lib/me'
import { Route } from '@/routes/app.$org'

/** The organization route inside the shell: its pages, or a not-found page for an organization the user cannot open. */
export function OrganizationLayout() {
  const { org } = Route.useParams()
  const me = useMe()
  // AppLayout (routes/app.tsx) shows the loading state before the shell exists.
  if (!me.data) return null
  const membership = me.data.memberships.find((item) => item.org.slug === org)
  if (!membership) return <OrganizationNotFound me={me.data} />
  return <Outlet />
}

function OrganizationNotFound({ me }: { me: Me }) {
  return (
    <PageContainer width="narrow">
      <PageHeader title="Not found" description="We could not find that organisation" />
      {me.memberships.length > 0 ? (
        <nav aria-label="Available organisations">
          <p className="mb-2 text-sm font-medium">Available organisations</p>
          <ul className="flex flex-col gap-2">
            {me.memberships.map((membership) => (
              <li key={membership.org.id}>
                <Link
                  className="text-sm text-primary underline-offset-4 hover:underline"
                  to={organizationHref(membership)}
                >
                  {membership.org.name}
                </Link>
              </li>
            ))}
          </ul>
        </nav>
      ) : null}
      <Link className="text-sm font-medium text-primary underline-offset-4 hover:underline" to="/app">
        Dashboard
      </Link>
    </PageContainer>
  )
}
```

- [ ] **Step 5: Turn the account, admin, index and landing frames into content**

`web/src/routes/app.account.tsx`: replace lines 1-28 (imports through the end of `AccountLayout`) with:

```tsx
import { createFileRoute, Link, Outlet, redirect, useRouter } from '@tanstack/react-router'
import { PageContainer } from '@/components/PageContainer'
import { DeletedOrgsList } from '@/features/lifecycle/DeletedOrgsList'
import { queryClient } from '@/lib/queryClient'
import { meQueryOptions } from './app.$org'

export const Route = createFileRoute('/app/account')({
  beforeLoad: async ({ location }) => {
    if (location.pathname === '/app/account' || location.pathname === '/app/account/') {
      throw redirect({ to: '/app/account/profile' })
    }
    await queryClient.ensureQueryData(meQueryOptions)
  },
  component: AccountLayout,
})

// Content inside the app shell: the account section tabs, the page, and restorable organizations.
function AccountLayout() {
  const router = useRouter()
  return (
    <PageContainer width="narrow">
      <AccountSettingsNavigation />
      <Outlet />
      <DeletedOrgsList navigate={(path) => router.history.push(path)} />
    </PageContainer>
  )
}
```

Leave `AccountSettingsNavigation` unchanged (`settings-navigation.test.tsx` imports it).

`web/src/routes/app.admin.tsx`: replace the import block and `AdminLayout` so the file reads:

```tsx
import { createFileRoute, Link, Outlet, redirect } from '@tanstack/react-router'

import { PageContainer } from '@/components/PageContainer'
import { queryClient } from '@/lib/queryClient'
import { en } from '@/messages/en'
import { meQueryOptions } from './app.$org'

export const Route = createFileRoute('/app/admin')({
  beforeLoad: async ({ location }) => {
    const me = await queryClient.ensureQueryData(meQueryOptions)
    if (!me.user.instance_admin) throw redirect({ to: '/app' })
    if (location.pathname === '/app/admin' || location.pathname === '/app/admin/') throw redirect({ to: '/app/admin/users' })
  },
  component: AdminLayout,
})

const sections = [
  ['/app/admin/users', en.admin.users],
  ['/app/admin/organizations', en.admin.organizations],
  ['/app/admin/sites', en.admin.sites],
  ['/app/admin/settings', en.admin.settings],
  ['/app/admin/security', en.admin.security],
] as const

// Content inside the app shell: the administration section tabs and the page.
function AdminLayout() {
  return (
    <PageContainer width="narrow">
      <nav aria-label={en.admin.nav} className="flex flex-wrap gap-1 border-b pb-3">
        {sections.map(([to, label]) => (
          <Link key={to} to={to} className="inline-flex min-h-11 items-center rounded-md px-3 text-sm hover:bg-muted" activeProps={{ className: 'bg-muted font-medium', 'aria-current': 'page' }}>{label}</Link>
        ))}
      </nav>
      <Outlet />
    </PageContainer>
  )
}
```

`sections` is read at module scope, as it was before. It is unchanged here and is not new code.

`web/src/routes/app.index.tsx`: delete `import { AccountFrame } from '@/components/AccountFrame'`. Replace

```tsx
  if (!membership) {
    return (
      <AccountFrame me={me.data}>
        <NoOrgScreen navigate={navigate} />
      </AccountFrame>
    )
  }
```

with `  if (!membership) return <NoOrgScreen navigate={navigate} />`. Replace

```tsx
  return (
    <AccountFrame me={me.data}><Suspense fallback={<p className="text-sm text-muted-foreground">{en.loadingDashboard}</p>}><Onboarding me={me.data} navigate={navigate} /></Suspense></AccountFrame>
  )
```

with

```tsx
  return (
    <Suspense fallback={<p className="text-sm text-muted-foreground" role="status">{en.loadingDashboard}</p>}><Onboarding me={me.data} navigate={navigate} /></Suspense>
  )
```

`web/src/features/sites/SitesOverviewLanding.tsx`: delete `import { AccountFrame } from '@/components/AccountFrame'`. Remove the `<AccountFrame me={me}>` and `</AccountFrame>` wrapper lines so the function returns `<SitesOverview ... />` directly (the props are unchanged).

- [ ] **Step 6: Remove the dead frame and menu**

```bash
git rm web/src/components/AccountFrame.tsx
```

`web/src/components/HeaderMenus.tsx`: delete the whole `export function HeaderMenus(...) { ... }` (lines 53-87). Update the imports to:

```tsx
import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { en } from '@/messages/en'
import { LogOutIcon, ShieldCheckIcon, UserRoundIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem,
  DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { requestLogout } from '@/lib/session-lifecycle'
import type { Me } from '@/lib/me'
import { ui } from '@/messages/ui'
```

Replace the `AccountMenu` doc comment with:

```tsx
/**
 * Identity, build version, account settings, instance administration and log out.
 * The sidebar footer opens it with its own trigger.
 */
```

`web/src/components/sticky-offsets.test.ts`: delete the line `  'src/components/AccountFrame.tsx: sticky top-0',`. Replace the comment line `// Anything that sticks inside a page must clear it; dialogs and AccountFrame have their own scroll context.` with `// Anything that sticks inside a page must clear it; dialogs have their own scroll context.`

`web/src/routes/-page-frame-adoption.test.ts`: delete these two lines from `OUTSIDE_SHELL`:

```ts
  // Not-found screen for an unknown organisation: renders outside the shell with its own <main>.
  'src/components/OrganizationLayout.tsx',
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd web && bun run typecheck && bun run test src/components src/routes src/features`
Expected: typecheck exits 0. All tests pass, including `AppShell.test`, `-app-shell-scope.test`, `-$org.test` (not-found links and the valid-organization shell), `HeaderMenus.test`, `sticky-offsets.test`, `-page-frame-adoption.test`, `settings-navigation.test` and `NoOrgScreen.test`. If `grep -rn "AccountFrame\|HeaderMenus }" web/src` prints anything, remove that reference.

- [ ] **Step 8: Commit**

```bash
git add -A web/src/routes/app.tsx web/src/components/AppShell.tsx web/src/components/AppShell.test.tsx web/src/components/OrganizationLayout.tsx web/src/routes/app.account.tsx web/src/routes/app.admin.tsx web/src/routes/app.index.tsx web/src/features/sites/SitesOverviewLanding.tsx web/src/components/HeaderMenus.tsx web/src/components/HeaderMenus.test.tsx web/src/components/AccountFrame.tsx web/src/components/sticky-offsets.test.ts web/src/routes/-page-frame-adoption.test.ts web/src/routes/-app-shell-scope.test.tsx
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'
git commit -m "feat(web): render account, admin and every /app route inside one app shell"
```

---

### Task 4: One main landmark and PageHeader on the remaining in-shell screens, guards, e2e, Group A gate

**Files:**
- Modify: `web/src/features/lifecycle/NoOrgScreen.tsx` (whole file)
- Modify: `web/src/features/sites/Onboarding.tsx:1-46`
- Modify: `web/src/routes/app.index.tsx:29-38,58-67`
- Modify: `web/src/features/sites/SitesOverviewLanding.tsx:23`
- Modify: `web/src/routes/-page-frame-adoption.test.ts:7-18`
- Create: `web/src/components/landmarks.test.ts`
- Test: `web/src/routes/-app-shell-scope.test.tsx`
- E2e (edited, not run): `web/e2e/account-navigation.spec.ts`, `navigation.spec.ts`, `not-found.spec.ts`, `sites-overview.spec.ts`, `a11y-audit.spec.ts`

**Interfaces:**
- Consumes: the `/app` shell from Task 3 and `PageContainer`/`PageHeader` (`web/src/components/PageContainer.tsx`, `PageHeader.tsx`).
- Produces: no `<main` outside `AppShell.tsx`, `routes/app.tsx`, `AuthScreen.tsx`, `SharedDashboard.tsx` and `lib/session-lifecycle.tsx`. `OUTSIDE_SHELL` lists only `AuthScreen.tsx` and `SharedDashboard.tsx`.

- [ ] **Step 1: Write the failing guards and tests**

Create `web/src/components/landmarks.test.ts`:

```ts
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { expect, it } from 'vitest'

const web = join(__dirname, '..', '..') // web/

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return sourceFiles(path)
    return /\.tsx$/.test(name) && !/\.test\./.test(name) ? [path] : []
  })
}

// Every /app page renders inside AppShell, which owns the only <main> (id="main-content").
// Only screens that never render inside the shell may own one.
const OWN_MAIN = [
  'src/components/AppShell.tsx',
  'src/features/auth/AuthScreen.tsx', // sign-in, sign-up, reset: outside /app
  'src/features/share/SharedDashboard.tsx', // public share page: outside /app
  'src/lib/session-lifecycle.tsx', // log-out screen replaces the whole app
  'src/routes/app.tsx', // loading state before the shell exists
]

it('keeps <main> to the shell and the screens outside it', () => {
  const found = sourceFiles(join(web, 'src'))
    .filter((file) => /<main\s/.test(readFileSync(file, 'utf8')))
    .map((file) => relative(web, file))
  expect(found.sort()).toEqual([...OWN_MAIN].sort())
})

it('sizes nothing against the removed 56px account header', () => {
  for (const file of sourceFiles(join(web, 'src'))) {
    expect(readFileSync(file, 'utf8'), relative(web, file)).not.toMatch(/100dvh-3\.5rem|\bh-14\b/)
  }
})
```

In `web/src/routes/-page-frame-adoption.test.ts`, replace the whole `OUTSIDE_SHELL` declaration (the comment above it and the `new Set` block) with:

```ts
// Files that render outside AppShell (sign-in and the public share page):
// they own their layout and keep a hand-rolled h1.
const OUTSIDE_SHELL = new Set<string>([
  'src/features/auth/AuthScreen.tsx',
  'src/features/share/SharedDashboard.tsx',
])
```

In `web/src/routes/-app-shell-scope.test.tsx`, add before the final `})`:

```tsx

  it('renders the no-organization screen inside the shell', async () => {
    renderRoute('/app', { ...me, memberships: [] })
    expect(await screen.findByRole('heading', { level: 1, name: "You don't belong to an organization" })).toBeVisible()
    const { sidebar } = await expectShell()
    expect(screen.getByRole('link', { name: 'Create organization' })).toHaveAttribute('href', '/app/account/organizations/new')
    expect(within(sidebar).getByRole('navigation', { name: 'Account' })).toBeInTheDocument()
  })

  it('renders first-site onboarding inside the shell', async () => {
    renderRoute('/app', { ...me, memberships: [{ ...acme, sites: [] }] })
    expect(await screen.findByRole('heading', { level: 1, name: 'Create your first site' })).toBeVisible()
    await expectShell()
  })
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && bun run test src/components/landmarks.test.ts src/routes/-page-frame-adoption.test.ts src/routes/-app-shell-scope.test.tsx`
Expected: FAIL.
- `landmarks` finds extra `<main` in `Onboarding.tsx`, `app.index.tsx` and `SitesOverviewLanding.tsx`, plus `100dvh-3.5rem` in `Onboarding.tsx`.
- `page-frame-adoption` lists `NoOrgScreen.tsx`, `Onboarding.tsx` and `app.index.tsx` as hand-rolled h1s.
- The onboarding scope test finds two `main` elements.

- [ ] **Step 3: Convert the screens**

Replace `web/src/features/lifecycle/NoOrgScreen.tsx` with:

```tsx
import { Link } from '@tanstack/react-router'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { DeletedOrgsList } from './DeletedOrgsList'

export function NoOrgScreen({ navigate }: { navigate: (path: string) => void }) {
  return (
    <PageContainer width="narrow">
      <PageHeader
        title="You don't belong to an organization"
        description="Create one to start collecting analytics, or restore one below."
        actions={<Button asChild><Link to="/app/account/organizations/new">Create organization</Link></Button>}
      />
      <DeletedOrgsList navigate={navigate} />
    </PageContainer>
  )
}
```

`web/src/features/sites/Onboarding.tsx`: add `import { PageContainer } from '@/components/PageContainer'` and `import { PageHeader } from '@/components/PageHeader'` after the `useEffect` import. Replace everything from `  if (!membership) return <section` to the end of the function with:

```tsx
  if (!membership) {
    return (
      <PageContainer width="narrow">
        <PageHeader title="No organization access" description="Ask an organization owner for an invitation. Your account settings are available from the Account menu." />
      </PageContainer>
    )
  }

  if (existingSite || !canCreateSites(membership.role)) return <p className="text-sm text-muted-foreground" role="status">{en.loadingDashboard}</p>

  // Inside the shell's main: no viewport-height math, the card sits centred below the 44px header.
  return (
    <div className="flex justify-center py-6 md:py-10">
      <section className="flex w-full max-w-md flex-col gap-5 rounded-xl border bg-card p-6 text-card-foreground shadow-sm">
        {me.demo_prompt_dismissed === false ? <DemoChoice org={{ id: membership.org.id, slug: membership.org.slug }} onDismissed={() => navigate(`/app/${membership.org.slug}/sites`)} /> : null}
        <PageHeader title="Create your first site" description="Add the website where Supermetry should collect analytics." />
        <SiteForm
          submitLabel="Create site"
          onSubmit={async (input) => {
            const site = await (createSite ?? ((value) => createManagedSite(value, membership.org.id)))(input)
            navigate(`/app/${membership.org.slug}/${site.public_id}/settings/installation`)
          }}
          secondaryAction={me.demo_prompt_dismissed !== false ? <Button type="button" variant="ghost" onClick={() => navigate(`/app/${membership.org.slug}/sites`)}>Skip for now</Button> : undefined}
        />
      </section>
    </div>
  )
}
```

`web/src/routes/app.index.tsx`: add `import { PageContainer } from '@/components/PageContainer'` and `import { PageHeader } from '@/components/PageHeader'` after the `en` import. Replace

```tsx
  if (!me.data) {
    return (
      <main className="grid min-h-dvh place-items-center px-6">
        <p className="text-sm text-muted-foreground">{en.loadingDashboard}</p>
      </main>
    )
  }
```

with `  if (!me.data) return <p className="text-sm text-muted-foreground" role="status">{en.loadingDashboard}</p>`. Replace the `OnboardingError` function with:

```tsx
function OnboardingError({ detail }: { detail: string }) {
  return (
    <PageContainer width="narrow">
      <PageHeader title={en.appName} />
      <p role="alert" className="text-sm text-muted-foreground">{detail}</p>
    </PageContainer>
  )
}
```

`web/src/features/sites/SitesOverviewLanding.tsx`: replace

```tsx
  if (target) return <main className="grid min-h-dvh place-items-center px-6"><p className="text-sm text-muted-foreground">{en.loadingDashboard}</p></main>
```

with

```tsx
  if (target) return <p className="text-sm text-muted-foreground" role="status">{en.loadingDashboard}</p>
```

- [ ] **Step 4: Run the guards and affected suites**

Run: `cd web && bun run test src/components/landmarks.test.ts src/routes/-page-frame-adoption.test.ts src/routes/-app-shell-scope.test.tsx src/routes/-index.test.tsx src/features/lifecycle/NoOrgScreen.test.tsx`
Expected: all PASS.

- [ ] **Step 5: Edit the e2e specs (edited, not run)**

`web/e2e/account-navigation.spec.ts`: after `  await expect(page.getByRole('navigation', { name: 'Account settings' })).toBeVisible()` (first test), insert:

```ts
  // Account pages render inside the same shell: one 44px header with breadcrumbs, the sidebar and one main.
  const accountHeader = page.getByRole('banner', { name: 'Application header' })
  expect((await accountHeader.boundingBox())?.height).toBe(44)
  await expect(accountHeader.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: 'Account', exact: true })).toHaveAttribute('href', '/app/account')
  await expect(page.getByRole('main')).toHaveCount(1)
  const accountLinks = page.getByRole('complementary', { name: 'Application sidebar' }).getByRole('navigation', { name: 'Account', exact: true })
  await expect(accountLinks.getByRole('link', { name: 'Account settings', exact: true })).toHaveAttribute('aria-current', 'page')
```

In the `no-site and no-organization users ...` test, after `  await expect(page.getByRole('heading', { name: "You don't belong to an organization" })).toBeVisible()`, insert:

```ts
  await expect(page.getByRole('banner', { name: 'Application header' })).toBeVisible()
  await expect(page.getByRole('main')).toHaveCount(1)
  await expect(page.getByRole('navigation', { name: 'Account', exact: true }).getByRole('link', { name: 'Account settings', exact: true })).toBeVisible()
```

`web/e2e/navigation.spec.ts`: add after the first test:

```ts
test('keeps the same shell mounted between a site and the account pages', async ({ page, owner }) => {
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}`
  await page.setViewportSize({ width: 1440, height: 900 })
  await owner.login(page, dashboard)
  const shell = page.locator('[data-slot="sidebar-wrapper"]')
  await shell.evaluate((element) => {
    ;(element as HTMLElement & { supermetryShellToken?: string }).supermetryShellToken = 'persistent'
  })
  let documentRequests = 0
  page.on('request', (request) => {
    if (request.resourceType() === 'document') documentRequests += 1
  })
  await page.getByRole('button', { name: 'Account', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Account settings', exact: true }).click()
  await expect(page).toHaveURL('/app/account/profile')
  expect((await page.getByRole('banner', { name: 'Application header' }).boundingBox())?.height).toBe(44)
  await expect(page.getByRole('main')).toHaveCount(1)
  const token = () => shell.evaluate((element) => (element as HTMLElement & { supermetryShellToken?: string }).supermetryShellToken)
  expect(await token()).toBe('persistent')
  await page.goBack()
  await expect(page).toHaveURL((url) => url.pathname === dashboard)
  expect(await token()).toBe('persistent')
  expect(documentRequests).toBe(0)
})
```

`web/e2e/not-found.spec.ts`: before the last line `  await expect(page).not.toHaveURL(/\/login/)`, insert:

```ts
  await expect(page.getByRole('banner', { name: 'Application header' })).toBeVisible()
  await expect(page.getByRole('main')).toHaveCount(1)
```

`web/e2e/sites-overview.spec.ts`: after `  await expect(page.getByRole('heading', { name: 'Sites', exact: true })).toBeVisible()` (the first one, right after `owner.login(page, '/app')`), insert:

```ts
  await expect(page.getByRole('banner', { name: 'Application header' })).toBeVisible()
  await expect(page.getByRole('main')).toHaveCount(1)
```

`web/e2e/a11y-audit.spec.ts`: in `reports axe findings for the requested pages`, after `  reports.push(await audit(page, 'settings'))`, insert:

```ts

  await page.goto('/app/account/profile')
  await expect(page.getByRole('heading', { level: 1, name: 'Profile' })).toBeVisible()
  await expect(page.getByRole('main')).toHaveCount(1)
  reports.push(await audit(page, 'account'))
```

`web/tsconfig.json` includes only `src`, so no typecheck covers `e2e/`. Re-read each edited hunk with `git diff web/e2e` and check that every selector matches a name pinned by the Vitest tests above. Do not run Playwright.

- [ ] **Step 6: Group A gate**

Run: `cd web && bun run typecheck && bun run test 2>&1 | tail -8 && bun run build 2>&1 | tail -3`
Expected:
- The typecheck exits 0.
- Tests: the only failure is the baseline `packages/tokens` test.
- The build's last line reads `Initial JavaScript: N / 256000 bytes gzipped` with N ≤ 256000. Expect N close to 220271, because the shell moved from the lazy `$org` route chunk to the lazy `/app` route chunk. Record N.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/lifecycle/NoOrgScreen.tsx web/src/features/sites/Onboarding.tsx web/src/routes/app.index.tsx web/src/features/sites/SitesOverviewLanding.tsx web/src/routes/-page-frame-adoption.test.ts web/src/components/landmarks.test.ts web/src/routes/-app-shell-scope.test.tsx web/e2e/account-navigation.spec.ts web/e2e/navigation.spec.ts web/e2e/not-found.spec.ts web/e2e/sites-overview.spec.ts web/e2e/a11y-audit.spec.ts
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'
git commit -m "fix(web): keep one main landmark and PageHeader on onboarding, no-org and landing screens" -m "Initial JavaScript: N / 256000 bytes gzipped. E2e specs account-navigation, navigation, not-found, sites-overview and a11y-audit edited, not run (the e2e webServer cannot log in to the DB here)."
```

Replace `N` with the measured number. **Group A ends here (spec step 7: one PR).**

---

## Group B: Command palette (spec step 8, Tasks 5-8)

### Task 5: Ctrl/⌘ chords in useShortcuts

**Files:**
- Modify: `web/src/lib/shortcuts.ts` (whole file)
- Test: `web/src/lib/shortcuts.test.tsx`

**Interfaces:**
- Produces: `useShortcuts(map)` now also accepts `mod+<key>` entries. They fire for Ctrl or Cmd (never with Alt), including while focus is in a field. Plain keys behave as before: they are ignored with any modifier and while editing.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/lib/shortcuts.test.tsx`, inside the `describe` block before its closing `})`:

```tsx

  it('fires mod+ chords for Ctrl or Cmd, also while typing, but never with Alt or for plain-key entries', () => {
    const onK = vi.fn()
    const onM = vi.fn()
    function ChordProbe() {
      useShortcuts({ 'mod+k': onK, m: onM })
      return <input aria-label="Filter" />
    }
    render(<ChordProbe />)
    const input = screen.getByRole('textbox')

    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    fireEvent.keyDown(document, { key: 'K', metaKey: true })
    input.focus()
    fireEvent.keyDown(input, { key: 'k', ctrlKey: true })
    expect(onK).toHaveBeenCalledTimes(3)

    fireEvent.keyDown(document, { key: 'k' })
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true, altKey: true })
    fireEvent.keyDown(document, { key: 'm', ctrlKey: true })
    expect(onK).toHaveBeenCalledTimes(3)
    expect(onM).not.toHaveBeenCalled()
  })

  it('prevents the browser default only for a registered chord', () => {
    function ChordProbe() {
      useShortcuts({ 'mod+k': () => undefined })
      return null
    }
    render(<ChordProbe />)
    const handled = new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, cancelable: true, bubbles: true })
    document.dispatchEvent(handled)
    expect(handled.defaultPrevented).toBe(true)
    const other = new KeyboardEvent('keydown', { key: 'p', ctrlKey: true, cancelable: true, bubbles: true })
    document.dispatchEvent(other)
    expect(other.defaultPrevented).toBe(false)
  })
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && bun run test src/lib/shortcuts.test.tsx`
Expected: FAIL. `onK` is called 0 times, because Ctrl and Cmd events are ignored today.

- [ ] **Step 3: Implement**

Replace `web/src/lib/shortcuts.ts` with:

```ts
import { useEffect, useRef } from 'react'

function isEditing(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  return (
    target instanceof HTMLInputElement ||
    target instanceof HTMLTextAreaElement ||
    target.isContentEditable
  )
}

/** The map key for a keydown: the lower-cased key, or `mod+<key>` while Ctrl or Cmd is held. Alt combinations are never shortcuts. */
function shortcutKey(event: KeyboardEvent): string | undefined {
  if (event.altKey) return undefined
  const key = event.key.toLowerCase()
  return event.ctrlKey || event.metaKey ? `mod+${key}` : key
}

/**
 * Document-wide shortcuts. Plain keys (`d`, `?`) are ignored while the user types in a field.
 * `mod+` chords (Ctrl or Cmd, e.g. `mod+k`) also fire from inside a field.
 */
export function useShortcuts(map: Record<string, () => void>) {
  const mapRef = useRef(map)
  mapRef.current = map

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const key = shortcutKey(event)
      if (!key) return
      if (!key.startsWith('mod+') && isEditing(event.target)) return
      const action = mapRef.current[key]
      if (!action) return
      event.preventDefault()
      action()
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [])
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web && bun run test src/lib/shortcuts.test.tsx src/components/AppShell.test.tsx "src/routes/-dashboard-breakdowns.test.tsx"`
Expected: PASS. The existing `m`/`d`/`?` behaviour is unchanged.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/shortcuts.ts web/src/lib/shortcuts.test.tsx
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'
git commit -m "feat(web): support Ctrl/Cmd chords in useShortcuts"
```

---

### Task 6: Palette entries from the sidebar's own gating

**Files:**
- Create: `web/src/components/paletteCommands.ts`
- Test: `web/src/components/paletteCommands.test.ts`

**Interfaces:**
- Consumes: `visibleNavigation`, `navigationHref`, `activeSite`, `settingsDestinations`, `organizationHref` from `./Sidebar`; `accountDestinations` from `./AccountSidebar` (Task 2); `hasCapability` from `@/lib/authorization`; `isAnonymousTracking` from `@/lib/tracking-policy`; `siteSlug` from `@/lib/matchSite`.
- Produces:

```ts
export const paletteGroups: readonly ['pages', 'sites', 'organizations', 'actions']
export type PaletteGroup = 'pages' | 'sites' | 'organizations' | 'actions'
export type PaletteAction = 'theme' | 'help' | 'logout'
export type PaletteCommand =
  | { id: string; group: PaletteGroup; label: string; detail?: string; keywords: string[]; kind: 'navigate'; href: string }
  | { id: string; group: PaletteGroup; label: string; detail?: string; keywords: string[]; kind: 'action'; action: PaletteAction }
export function paletteCommands(input: { me: Me; membership?: Membership; requestedSite?: string; theme: Theme }): PaletteCommand[]
```

- [ ] **Step 1: Write the failing test**

Create `web/src/components/paletteCommands.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import type { Me, Membership, Site } from '@/lib/me'

import { anonymousRouteRestricted } from './AnalyticsPrivacyBoundary'
import { paletteCommands, type PaletteCommand } from './paletteCommands'

const docs: Site = { id: 7, name: 'Docs', slug: 'docs', public_id: 'docs-public', tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }
const shop: Site = { id: 8, name: 'Shop', slug: 'shop', tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }
const acme: Membership = { org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner', sites: [docs, shop], effective_capabilities: ['profiles.read', 'replay.read'] }
const other: Membership = { org: { id: 2, name: 'Other', slug: 'other' }, role: 'viewer', sites: [] }
const me: Me = { user: { id: 1, name: 'Owner', email: 'owner@example.test' }, memberships: [acme, other] }

const run = (input: Partial<Parameters<typeof paletteCommands>[0]> = {}) =>
  paletteCommands({ me, membership: acme, requestedSite: 'docs', theme: 'light', ...input })
const labels = (commands: PaletteCommand[], group: PaletteCommand['group']) =>
  commands.filter((command) => command.group === group).map((command) => command.label)
const hrefOf = (commands: PaletteCommand[], label: string) => {
  const command = commands.find((item) => item.label === label)
  return command?.kind === 'navigate' ? command.href : undefined
}

describe('paletteCommands', () => {
  it('mirrors the sidebar, settings and account menu for an owner on an identified site', () => {
    const commands = run()
    expect(labels(commands, 'pages')).toEqual([
      'Dashboard', 'Events', 'Sessions', 'Realtime', 'Replays', 'Journeys', 'Retention', 'Profiles', 'Cohorts',
      'Heatmaps', 'Web Vitals', 'Funnels', 'Goals', 'Revenue', 'Sites', 'Links and pixels', 'Data health',
      'Site settings', 'Organization settings', 'Account settings',
    ])
    expect(hrefOf(commands, 'Dashboard')).toBe('/app/acme/docs')
    expect(hrefOf(commands, 'Events')).toBe('/app/acme/docs/events')
    expect(hrefOf(commands, 'Replays')).toBe('/app/acme/docs/replays')
    expect(hrefOf(commands, 'Links and pixels')).toBe('/app/acme/docs/links')
    expect(hrefOf(commands, 'Sites')).toBe('/app/acme/sites')
    expect(hrefOf(commands, 'Data health')).toBe('/app/acme/health')
    expect(hrefOf(commands, 'Site settings')).toBe('/app/acme/docs/settings/general')
    expect(hrefOf(commands, 'Organization settings')).toBe('/app/acme/settings')
    expect(hrefOf(commands, 'Account settings')).toBe('/app/account/profile')
  })

  it('gates pages by role and capability exactly like the sidebar', () => {
    const pages = labels(run({ membership: { ...acme, role: 'viewer', effective_capabilities: [] } }), 'pages')
    for (const hidden of ['Data health', 'Site settings', 'Profiles', 'Cohorts', 'Replays']) expect(pages).not.toContain(hidden)
    expect(pages).toContain('Organization settings')
    expect(pages).toContain('Events')
  })

  it('never offers a screen that anonymous tracking blocks', () => {
    const anonymous: Membership = {
      ...acme,
      sites: acme.sites.map((site) => ({ ...site, tracking_policy: { instance_mode: 'anonymous', effective_mode: 'anonymous', configured_profiles_enabled: true, restrictions: ['instance_anonymous'] } })),
    }
    const commands = run({ membership: anonymous })
    expect(labels(commands, 'pages')).toEqual(['Dashboard', 'Sites', 'Links and pixels', 'Data health', 'Site settings', 'Organization settings', 'Account settings'])
    for (const command of commands) {
      if (command.kind === 'navigate') expect(anonymousRouteRestricted(command.href), command.href).toBe(false)
    }
  })

  it('switches to the other sites of this organization and to other organizations', () => {
    const commands = run()
    expect(commands.filter((command) => command.group === 'sites')).toEqual([
      { id: 'site:8', group: 'sites', label: 'Shop', detail: 'Acme', keywords: ['Shop', 'Acme'], kind: 'navigate', href: '/app/acme/shop' },
    ])
    expect(commands.filter((command) => command.group === 'organizations')).toEqual([
      { id: 'org:2', group: 'organizations', label: 'Other', keywords: ['Other'], kind: 'navigate', href: '/app/other/settings' },
    ])
  })

  it.each(['docs', 'docs-public', '7'])('recognises the current site by the %s alias and links its canonical slug', (alias) => {
    const commands = run({ requestedSite: alias })
    expect(labels(commands, 'sites')).toEqual(['Shop'])
    expect(hrefOf(commands, 'Events')).toBe('/app/acme/docs/events')
  })

  it('offers account pages and every organization outside an organization', () => {
    const commands = run({ membership: undefined, requestedSite: undefined })
    expect(labels(commands, 'pages')).toEqual(['Account settings'])
    expect(labels(commands, 'sites')).toEqual([])
    expect(commands.filter((command) => command.group === 'organizations').map((command) => command.kind === 'navigate' && command.href)).toEqual(['/app/acme/docs', '/app/other/settings'])
    const admin = run({ me: { ...me, user: { ...me.user, instance_admin: true } }, membership: undefined })
    expect(labels(admin, 'pages')).toEqual(['Account settings', 'Instance administration'])
    expect(hrefOf(admin, 'Instance administration')).toBe('/app/admin/users')
  })

  it('offers only existing actions, labels theme for the current theme, and keeps ids unique', () => {
    expect(labels(run(), 'actions')).toEqual(['Use dark theme', 'Show keyboard shortcuts', 'Log out'])
    expect(labels(run({ theme: 'dark' }), 'actions')[0]).toBe('Use light theme')
    const ids = run().map((command) => command.id)
    expect(new Set(ids).size).toBe(ids.length)
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && bun run test src/components/paletteCommands.test.ts`
Expected: FAIL with `Failed to resolve import "./paletteCommands"`.

- [ ] **Step 3: Implement**

Create `web/src/components/paletteCommands.ts`:

```ts
import { hasCapability } from '@/lib/authorization'
import { siteSlug } from '@/lib/matchSite'
import type { Me, Membership } from '@/lib/me'
import type { Theme } from '@/lib/theme'
import { isAnonymousTracking } from '@/lib/tracking-policy'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'

import { accountDestinations } from './AccountSidebar'
import { activeSite, navigationHref, organizationHref, settingsDestinations, visibleNavigation } from './Sidebar'

export const paletteGroups = ['pages', 'sites', 'organizations', 'actions'] as const
export type PaletteGroup = (typeof paletteGroups)[number]
export type PaletteAction = 'theme' | 'help' | 'logout'

type PaletteBase = { id: string; group: PaletteGroup; label: string; detail?: string; keywords: string[] }
export type PaletteCommand =
  | (PaletteBase & { kind: 'navigate'; href: string })
  | (PaletteBase & { kind: 'action'; action: PaletteAction })

/**
 * The command palette entries: exactly what the sidebar, its switcher, the account menu and the
 * header already offer, with the same role, capability and anonymous-tracking gating. Ids are
 * unique because cmdk uses them as item values.
 */
export function paletteCommands({
  me,
  membership,
  requestedSite,
  theme,
}: {
  me: Me
  membership?: Membership
  requestedSite?: string
  theme: Theme
}): PaletteCommand[] {
  const commands: PaletteCommand[] = []
  const page = (id: string, label: string, href: string) =>
    commands.push({ id: `page:${id}`, group: 'pages', label, keywords: [label], kind: 'navigate', href })

  if (membership) {
    const org = membership.org.slug
    const site = activeSite(membership, requestedSite)
    const slug = site ? siteSlug(site) : ''
    const showReplays = Boolean(site && !isAnonymousTracking(site) && hasCapability(membership, 'replay.read'))
    for (const item of visibleNavigation(membership.role, Boolean(site), membership, site)) {
      page(item.suffix || '/', item.label, navigationHref(item, org, slug))
      if (item.suffix === '/realtime' && showReplays) page('/replays', ui.crumbs.segments.replays, `/app/${org}/${slug}/replays`)
    }
    for (const destination of settingsDestinations(membership.role, site)) {
      if (destination === 'site' && site) page('site-settings', ui.menus.siteSettings, `/app/${org}/${slug}/settings/general`)
      if (destination === 'org') page('org-settings', ui.menus.organizationSettings, `/app/${org}/settings`)
    }
    for (const other of membership.sites) {
      if (other.id === site?.id) continue
      commands.push({
        id: `site:${other.id}`,
        group: 'sites',
        label: other.name,
        detail: membership.org.name,
        keywords: [other.name, membership.org.name],
        kind: 'navigate',
        href: `/app/${org}/${siteSlug(other)}`,
      })
    }
  }

  for (const destination of accountDestinations(me)) {
    if (destination === 'admin') page('admin', en.admin.link, '/app/admin/users')
    else page('account', ui.menus.accountSettings, '/app/account/profile')
  }

  for (const other of me.memberships) {
    if (other.org.id === membership?.org.id) continue
    commands.push({ id: `org:${other.org.id}`, group: 'organizations', label: other.org.name, keywords: [other.org.name], kind: 'navigate', href: organizationHref(other) })
  }

  const actions: [PaletteAction, string][] = [
    ['theme', theme === 'dark' ? en.theme.dark : en.theme.light],
    ['help', en.shortcuts.open],
    ['logout', ui.menus.logOut],
  ]
  for (const [action, label] of actions) commands.push({ id: `action:${action}`, group: 'actions', label, keywords: [label], kind: 'action', action })

  return commands
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `cd web && bun run test src/components/paletteCommands.test.ts && bun run typecheck`
Expected: PASS, and the typecheck exits 0.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/paletteCommands.ts web/src/components/paletteCommands.test.ts
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'
git commit -m "feat(web): derive command palette entries from the sidebar's own gating"
```

---

### Task 7: CommandPalette on cmdk 1.1.1

**Files:**
- Modify: `web/package.json`, `web/bun.lock` (via `bun add`)
- Create: `web/src/components/ui/command.tsx`
- Create: `web/src/components/CommandPalette.tsx`
- Modify: `web/src/messages/ui.ts`, `web/src/messages/locales/de.ui.ts`, `web/src/messages/locales/es.ui.ts` (new `palette` block)
- Test: `web/src/components/CommandPalette.test.tsx`

**Interfaces:**
- Consumes: `PaletteCommand`, `paletteGroups` (Task 6).
- Produces: `CommandPalette({ open, onOpenChange, commands, onRun }: { open: boolean; onOpenChange: (open: boolean) => void; commands: PaletteCommand[]; onRun: (command: PaletteCommand) => void })`. It is a named export, presentational and router-free. Selecting an item calls `onRun`. The caller closes the dialog.
- Produces: the strings `ui.palette.title`, `.description`, `.inputLabel`, `.placeholder`, `.empty` and `.groups.{pages,sites,organizations,actions}`.

- [ ] **Step 1: Add cmdk, pinned, and verify the lockfile diff**

Run: `cd web && bun add --exact cmdk@1.1.1`
Then verify:

```bash
grep -n '"cmdk"' package.json          # expected: "cmdk": "1.1.1",  (no caret)
git diff -U0 bun.lock | grep -c '^+[^+]'   # expected: 2 (the workspace dependency line and the package entry)
git diff -U0 bun.lock | grep -E '^\+ {4}"'  # expected: exactly one line, starting  +    "cmdk": ["cmdk@1.1.1",
bun install --frozen-lockfile              # expected: no changes
```

If more package entries appear, they may only be cmdk's own dependencies (`@radix-ui/react-compose-refs`, `@radix-ui/react-dialog`, `@radix-ui/react-id`, `@radix-ui/react-primitive` and their dependencies). List them in the commit body. Anything else: stop and ask.

- [ ] **Step 2: Write the failing test**

Create `web/src/components/CommandPalette.test.tsx`:

```tsx
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { CommandPalette } from './CommandPalette'
import type { PaletteCommand } from './paletteCommands'

const commands: PaletteCommand[] = [
  { id: 'page:/events', group: 'pages', label: 'Events', keywords: ['Events'], kind: 'navigate', href: '/app/acme/docs/events' },
  { id: 'page:/funnels', group: 'pages', label: 'Funnels', keywords: ['Funnels'], kind: 'navigate', href: '/app/acme/docs/funnels' },
  { id: 'site:8', group: 'sites', label: 'Shop', detail: 'Acme', keywords: ['Shop', 'Acme'], kind: 'navigate', href: '/app/acme/shop' },
  { id: 'action:theme', group: 'actions', label: 'Use dark theme', keywords: ['Use dark theme'], kind: 'action', action: 'theme' },
]

function Harness({ onRun }: { onRun: (command: PaletteCommand) => void }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>Open palette</button>
      <CommandPalette
        open={open}
        onOpenChange={setOpen}
        commands={commands}
        onRun={(command) => {
          setOpen(false)
          onRun(command)
        }}
      />
    </>
  )
}

beforeEach(() => {
  Element.prototype.scrollIntoView = () => undefined
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  })
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

async function open(onRun = vi.fn()) {
  render(<Harness onRun={onRun} />)
  const trigger = screen.getByRole('button', { name: 'Open palette' })
  trigger.focus()
  fireEvent.click(trigger)
  const dialog = await screen.findByRole('dialog', { name: 'Command palette' })
  const input = within(dialog).getByRole('combobox', { name: 'Search commands' })
  return { trigger, dialog, input, onRun }
}

describe('CommandPalette', () => {
  it('is a titled dialog with the search field focused and only non-empty groups', async () => {
    const { dialog, input } = await open()
    await waitFor(() => expect(input).toHaveFocus())
    expect(within(dialog).getByRole('group', { name: 'Pages' })).toBeInTheDocument()
    expect(within(dialog).getByRole('group', { name: 'Sites' })).toBeInTheDocument()
    expect(within(dialog).getByRole('group', { name: 'Actions' })).toBeInTheDocument()
    expect(within(dialog).queryByRole('group', { name: 'Organizations' })).toBeNull()
    expect(within(dialog).getByRole('option', { name: /Shop/ })).toHaveTextContent('Acme')
  })

  it('filters by label as the user types and shows an empty state', async () => {
    const { dialog, input } = await open()
    fireEvent.change(input, { target: { value: 'fun' } })
    await waitFor(() => expect(within(dialog).getAllByRole('option').map((option) => option.textContent)).toEqual(['Funnels']))
    fireEvent.change(input, { target: { value: 'zzzz' } })
    expect(await within(dialog).findByText('No matching commands')).toBeVisible()
  })

  it('moves the selection with the arrow keys and runs the selected command on Enter', async () => {
    const { dialog, input, onRun } = await open()
    await waitFor(() => expect(within(dialog).getByRole('option', { name: 'Events' })).toHaveAttribute('aria-selected', 'true'))
    fireEvent.keyDown(input, { key: 'ArrowDown' })
    expect(within(dialog).getByRole('option', { name: 'Funnels' })).toHaveAttribute('aria-selected', 'true')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onRun).toHaveBeenCalledWith(commands[1])
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument())
  })

  it('closes on Escape and returns focus to the invoker', async () => {
    const { trigger, input, onRun } = await open()
    fireEvent.keyDown(input, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument())
    await waitFor(() => expect(trigger).toHaveFocus())
    expect(onRun).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 3: Run it to verify it fails**

Run: `cd web && bun run test src/components/CommandPalette.test.tsx`
Expected: FAIL with `Failed to resolve import "./CommandPalette"`.

- [ ] **Step 4: Add the strings**

In `web/src/messages/ui.ts`, replace

```ts
      screen_size: 'Screen size', goal: 'Goal', event: 'Event', weekday_hour: 'Weekly rhythm',
    } as Record<string, string>,
  },
}
```

with

```ts
      screen_size: 'Screen size', goal: 'Goal', event: 'Event', weekday_hour: 'Weekly rhythm',
    } as Record<string, string>,
  },
  palette: {
    title: 'Command palette',
    description: 'Go to a page, switch site or organization, or run an action.',
    inputLabel: 'Search commands',
    placeholder: 'Type a page, site or action…',
    empty: 'No matching commands',
    groups: { pages: 'Pages', sites: 'Sites', organizations: 'Organizations', actions: 'Actions' },
  },
}
```

In `web/src/messages/locales/de.ui.ts`, replace

```ts
      screen_size: 'Bildschirmgröße', goal: 'Ziel', event: 'Ereignis', weekday_hour: 'Wochenrhythmus',
    },
  },
}
```

with

```ts
      screen_size: 'Bildschirmgröße', goal: 'Ziel', event: 'Ereignis', weekday_hour: 'Wochenrhythmus',
    },
  },
  palette: {
    title: 'Befehlspalette',
    description: 'Zu einer Seite gehen, Website oder Organisation wechseln oder eine Aktion ausführen.',
    inputLabel: 'Befehle durchsuchen',
    placeholder: 'Seite, Website oder Aktion eingeben…',
    empty: 'Keine passenden Befehle',
    groups: { pages: 'Seiten', sites: 'Websites', organizations: 'Organisationen', actions: 'Aktionen' },
  },
}
```

In `web/src/messages/locales/es.ui.ts`, replace

```ts
      screen_size: 'Tamaño de pantalla', goal: 'Objetivo', event: 'Evento', weekday_hour: 'Ritmo semanal',
    },
  },
}
```

with

```ts
      screen_size: 'Tamaño de pantalla', goal: 'Objetivo', event: 'Evento', weekday_hour: 'Ritmo semanal',
    },
  },
  palette: {
    title: 'Paleta de comandos',
    description: 'Ve a una página, cambia de sitio u organización o ejecuta una acción.',
    inputLabel: 'Buscar comandos',
    placeholder: 'Escribe una página, un sitio o una acción…',
    empty: 'No hay comandos coincidentes',
    groups: { pages: 'Páginas', sites: 'Sitios', organizations: 'Organizaciones', actions: 'Acciones' },
  },
}
```

- [ ] **Step 5: Write the shadcn command primitive**

Create `web/src/components/ui/command.tsx`:

```tsx
import * as React from 'react'
import { Command as CommandPrimitive } from 'cmdk'
import { SearchIcon } from 'lucide-react'

import { cn } from '@/lib/utils'

// shadcn/ui `command` (radix-nova), written by hand: `shadcn add command` would overwrite
// ui/button, ui/input, ui/textarea and ui/dialog and pull in input-group. Differences: a plain
// input row instead of input-group, no check icon on items, and no CommandDialog (CommandPalette
// composes ui/dialog so the dialog title lives inside the dialog).

function Command({ className, ...props }: React.ComponentProps<typeof CommandPrimitive>) {
  return (
    <CommandPrimitive
      data-slot="command"
      className={cn('flex size-full flex-col overflow-hidden rounded-xl bg-popover p-1 text-popover-foreground', className)}
      {...props}
    />
  )
}

function CommandInput({ className, ...props }: React.ComponentProps<typeof CommandPrimitive.Input>) {
  return (
    <div data-slot="command-input-wrapper" className="flex h-9 items-center gap-2 border-b px-2">
      <SearchIcon aria-hidden="true" className="size-4 shrink-0 opacity-50" />
      <CommandPrimitive.Input
        data-slot="command-input"
        className={cn('flex h-9 w-full bg-transparent text-sm outline-hidden placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50', className)}
        {...props}
      />
    </div>
  )
}

function CommandList({ className, ...props }: React.ComponentProps<typeof CommandPrimitive.List>) {
  return (
    <CommandPrimitive.List
      data-slot="command-list"
      className={cn('max-h-72 scroll-py-1 overflow-x-hidden overflow-y-auto outline-none', className)}
      {...props}
    />
  )
}

function CommandEmpty({ className, ...props }: React.ComponentProps<typeof CommandPrimitive.Empty>) {
  return <CommandPrimitive.Empty data-slot="command-empty" className={cn('py-6 text-center text-sm', className)} {...props} />
}

function CommandGroup({ className, ...props }: React.ComponentProps<typeof CommandPrimitive.Group>) {
  return (
    <CommandPrimitive.Group
      data-slot="command-group"
      className={cn(
        'overflow-hidden p-1 text-foreground **:[[cmdk-group-heading]]:px-2 **:[[cmdk-group-heading]]:py-1.5 **:[[cmdk-group-heading]]:text-xs **:[[cmdk-group-heading]]:font-medium **:[[cmdk-group-heading]]:text-muted-foreground',
        className,
      )}
      {...props}
    />
  )
}

function CommandItem({ className, ...props }: React.ComponentProps<typeof CommandPrimitive.Item>) {
  return (
    <CommandPrimitive.Item
      data-slot="command-item"
      className={cn(
        "relative flex cursor-default items-center gap-2 rounded-lg px-2 py-1.5 text-sm outline-hidden select-none data-[disabled=true]:pointer-events-none data-[disabled=true]:opacity-50 data-[selected=true]:bg-muted data-[selected=true]:text-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        className,
      )}
      {...props}
    />
  )
}

export { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList }
```

cmdk renders `data-selected="false"` on unselected items, so the selected style must use `data-[selected=true]:`, not `data-selected:`.

- [ ] **Step 6: Write `CommandPalette`**

Create `web/src/components/CommandPalette.tsx`:

```tsx
import { Building2Icon, CircleHelpIcon, FileTextIcon, Globe2Icon, LogOutIcon, SunMoonIcon } from 'lucide-react'

import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { ui } from '@/messages/ui'

import { paletteGroups, type PaletteCommand } from './paletteCommands'

function CommandIcon({ command }: { command: PaletteCommand }) {
  if (command.kind === 'action') {
    if (command.action === 'theme') return <SunMoonIcon aria-hidden="true" />
    if (command.action === 'help') return <CircleHelpIcon aria-hidden="true" />
    return <LogOutIcon aria-hidden="true" />
  }
  if (command.group === 'sites') return <Globe2Icon aria-hidden="true" />
  if (command.group === 'organizations') return <Building2Icon aria-hidden="true" />
  return <FileTextIcon aria-hidden="true" />
}

/**
 * The global command palette: the app's only global search. Presentational: the caller supplies
 * the commands (see paletteCommands) and runs the chosen one. Radix returns focus to the invoker on close.
 */
export function CommandPalette({
  open,
  onOpenChange,
  commands,
  onRun,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  commands: PaletteCommand[]
  onRun: (command: PaletteCommand) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="top-[20%] translate-y-0 gap-0 overflow-hidden p-0 sm:max-w-lg" showCloseButton={false}>
        <DialogTitle className="sr-only">{ui.palette.title}</DialogTitle>
        <DialogDescription className="sr-only">{ui.palette.description}</DialogDescription>
        <Command label={ui.palette.inputLabel}>
          <CommandInput placeholder={ui.palette.placeholder} />
          <CommandList>
            <CommandEmpty>{ui.palette.empty}</CommandEmpty>
            {paletteGroups.map((group) => {
              const items = commands.filter((command) => command.group === group)
              if (items.length === 0) return null
              return (
                <CommandGroup key={group} heading={ui.palette.groups[group]}>
                  {items.map((command) => (
                    <CommandItem key={command.id} keywords={command.keywords} value={command.id} onSelect={() => onRun(command)}>
                      <CommandIcon command={command} />
                      <span className="min-w-0 flex-1 truncate">{command.label}</span>
                      {command.detail ? <span className="truncate text-xs text-muted-foreground">{command.detail}</span> : null}
                    </CommandItem>
                  ))}
                </CommandGroup>
              )
            })}
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd web && bun run test src/components/CommandPalette.test.tsx src/messages/locales/locales.test.ts && bun run typecheck`
Expected: PASS, and the typecheck exits 0.

- [ ] **Step 8: Commit**

```bash
git add web/package.json web/bun.lock web/src/components/ui/command.tsx web/src/components/CommandPalette.tsx web/src/components/CommandPalette.test.tsx web/src/messages/ui.ts web/src/messages/locales/de.ui.ts web/src/messages/locales/es.ui.ts
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'
git commit -m "feat(web): add the command palette on cmdk 1.1.1" -m "New dependency: cmdk 1.1.1 (exact). bun.lock adds only the cmdk entry; its Radix dependencies were already locked."
```

---

### Task 8: Open the palette from the header and Ctrl/⌘ K; help list, budget, e2e

**Files:**
- Modify: `web/src/components/AppShell.tsx`
- Modify: `web/src/components/AppHeader.tsx`
- Modify: `web/src/lib/shortcuts.ts` (add `isApplePlatform`)
- Modify: `web/src/messages/en.ts:261`, `web/src/messages/locales/de.ts:263`, `web/src/messages/locales/es.ts:263` (`shortcuts.palette`)
- Modify: `web/src/messages/ui.ts`, `de.ui.ts`, `es.ui.ts` (`palette.open`, `palette.keyHint`, `palette.keyHintApple`)
- Test: `web/src/components/AppShell.test.tsx`, `web/src/components/AppHeader.test.tsx`, `web/src/lib/shortcuts.test.tsx`
- E2e (edited, not run): `web/e2e/navigation.spec.ts`, `web/e2e/a11y-audit.spec.ts`

**Interfaces:**
- Consumes: `useShortcuts` with `mod+k` (Task 5), `paletteCommands` (Task 6), `CommandPalette` (Task 7).
- Produces: `AppHeader` prop `onOpenPalette: () => void`, and `isApplePlatform(nav?: Pick<Navigator, 'platform' | 'userAgent'>): boolean` in `@/lib/shortcuts`.

- [ ] **Step 1: Write the failing tests**

`web/src/lib/shortcuts.test.tsx`: change the import to `import { isApplePlatform, useShortcuts } from './shortcuts'` and append after the `describe` block:

```tsx

describe('isApplePlatform', () => {
  it('detects Apple platforms for the ⌘ hint and falls back to Ctrl elsewhere', () => {
    expect(isApplePlatform({ platform: 'MacIntel', userAgent: '' })).toBe(true)
    expect(isApplePlatform({ platform: '', userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)' })).toBe(true)
    expect(isApplePlatform({ platform: 'Win32', userAgent: 'Mozilla/5.0 (Windows NT 10.0)' })).toBe(false)
    expect(isApplePlatform({ platform: 'Linux x86_64', userAgent: '' })).toBe(false)
    expect(isApplePlatform(undefined)).toBe(false)
  })
})
```

`web/src/components/AppHeader.test.tsx`:
1. Change the testing-library import to `import { cleanup, fireEvent, screen, within } from '@testing-library/react'`.
2. Replace the `show` function with:

```tsx
function show(path: string, onOpenPalette = vi.fn()) {
  renderWithRouter(
    <TooltipProvider>
      <SidebarProvider>
        <AppHeader theme="light" onToggleTheme={vi.fn()} onOpenHelp={vi.fn()} onOpenPalette={onOpenPalette} />
      </SidebarProvider>
    </TooltipProvider>,
    path,
  )
  return screen.findByRole('banner', { name: 'Application header' })
}
```

3. Add inside the `describe` block:

```tsx

  it('opens the command palette from a labelled trigger that announces its shortcut', async () => {
    const onOpenPalette = vi.fn()
    const header = await show('/app/account/profile', onOpenPalette)
    const trigger = within(header).getByRole('button', { name: 'Open command palette' })
    expect(trigger).toHaveAttribute('aria-keyshortcuts', 'Control+K Meta+K')
    fireEvent.click(trigger)
    expect(onOpenPalette).toHaveBeenCalledOnce()
  })
```

`web/src/components/AppShell.test.tsx`:
1. Change the testing-library import to `import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'`.
2. Replace the hoisted line and the `shortcuts` and `theme` mocks (lines 15 and 21-22) with:

```tsx
const { useLiveSummary, useReplayAvailability, privateRequest, shortcuts, toggleTheme, requestLogout } = vi.hoisted(() => ({
  useLiveSummary: vi.fn(),
  useReplayAvailability: vi.fn(),
  privateRequest: vi.fn(),
  shortcuts: { current: {} as Record<string, () => void> },
  toggleTheme: vi.fn(),
  requestLogout: vi.fn(),
}))
```

and

```tsx
vi.mock('@/lib/shortcuts', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/shortcuts')>(),
  useShortcuts: (map: Record<string, () => void>) => { shortcuts.current = map },
}))
vi.mock('@/lib/theme', () => ({ useTheme: () => ({ theme: 'light', toggle: toggleTheme }) }))
vi.mock('@/lib/session-lifecycle', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/session-lifecycle')>(),
  requestLogout,
}))
```

Keep the `@/features/realtime/useLiveSummary`, `@/components/replay/api` and `@/features/settings/privateApi` mocks.
3. In `beforeEach`, add `    Element.prototype.scrollIntoView = () => undefined` as the first line.
4. In `lists every registered keyboard shortcut in the help dialog`, replace the expected array with:

```tsx
    expect(within(dialog).getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      'D — Toggle dark mode',
      '? — Show this help',
      'Ctrl/⌘ B — Collapse or expand the sidebar',
      'Ctrl/⌘ K — Open the command palette',
      'M — Show the last 30 days (dashboard)',
      'C — Turn period comparison on or off (dashboard)',
    ])
```

5. Add at the end of the `describe` block:

```tsx
  it('opens the command palette from the header trigger and returns focus to it on Escape', async () => {
    renderShell('/app/acme/docs/settings/general')
    const header = await screen.findByRole('banner', { name: 'Application header' })
    const trigger = within(header).getByRole('button', { name: 'Open command palette' })
    trigger.focus()
    fireEvent.click(trigger)
    const dialog = await screen.findByRole('dialog', { name: 'Command palette' })
    const input = within(dialog).getByRole('combobox', { name: 'Search commands' })
    await waitFor(() => expect(input).toHaveFocus())
    fireEvent.keyDown(input, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument())
    await waitFor(() => expect(trigger).toHaveFocus())
  })

  it('registers Ctrl/⌘ K and navigates to a page from the palette', async () => {
    const { router } = renderShell('/app/acme/docs/settings/general')
    await screen.findByRole('banner', { name: 'Application header' })
    act(() => shortcuts.current['mod+k']())
    const dialog = await screen.findByRole('dialog', { name: 'Command palette' })
    const input = within(dialog).getByRole('combobox', { name: 'Search commands' })
    fireEvent.change(input, { target: { value: 'Realtime' } })
    await waitFor(() => expect(within(dialog).getByRole('option', { name: 'Realtime' })).toHaveAttribute('aria-selected', 'true'))
    fireEvent.keyDown(input, { key: 'Enter' })
    await waitFor(() => expect(router.state.location.pathname).toBe('/app/acme/docs/realtime'))
    expect(await screen.findByRole('heading', { name: 'Realtime page' })).toBeVisible()
    expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument()
  })

  it('runs only the existing actions: theme, shortcut help and log out', async () => {
    renderShell('/app/acme/docs/settings/general')
    const openPalette = async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Open command palette' }))
      return screen.findByRole('dialog', { name: 'Command palette' })
    }
    const actions = within(await openPalette()).getByRole('group', { name: 'Actions' })
    expect(within(actions).getAllByRole('option').map((option) => option.textContent)).toEqual(['Use dark theme', 'Show keyboard shortcuts', 'Log out'])
    fireEvent.click(within(actions).getByRole('option', { name: 'Use dark theme' }))
    expect(toggleTheme).toHaveBeenCalledOnce()
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument())

    fireEvent.click(within(await openPalette()).getByRole('option', { name: 'Show keyboard shortcuts' }))
    expect(await screen.findByRole('dialog', { name: 'Keyboard shortcuts' })).toBeVisible()
    fireEvent.keyDown(screen.getByRole('dialog', { name: 'Keyboard shortcuts' }), { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Keyboard shortcuts' })).not.toBeInTheDocument())

    fireEvent.click(within(await openPalette()).getByRole('option', { name: 'Log out' }))
    expect(requestLogout).toHaveBeenCalledOnce()
  })

  it('offers no blocked analytics screen in the palette for an anonymous site', async () => {
    const anonymousMembership: Membership = { ...membership, sites: membership.sites.map((site) => ({ ...site, tracking_policy: { instance_mode: 'anonymous', effective_mode: 'anonymous', configured_profiles_enabled: true, restrictions: ['instance_anonymous'] } })) }
    renderShell('/app/acme/docs-public/settings/general', anonymousMembership)
    fireEvent.click(await screen.findByRole('button', { name: 'Open command palette' }))
    const pages = within(await screen.findByRole('dialog', { name: 'Command palette' })).getByRole('group', { name: 'Pages' })
    const names = within(pages).getAllByRole('option').map((option) => option.textContent)
    expect(names).toContain('Dashboard')
    for (const label of ['Events', 'Sessions', 'Profiles', 'Replays', 'Journeys', 'Retention', 'Heatmaps', 'Web Vitals', 'Funnels', 'Realtime', 'Goals', 'Revenue', 'Cohorts']) {
      expect(names).not.toContain(label)
    }
  })

  it('limits the palette to account pages and organizations outside an organization', async () => {
    renderAppShell('/app/account/profile')
    fireEvent.click(await screen.findByRole('button', { name: 'Open command palette' }))
    const dialog = await screen.findByRole('dialog', { name: 'Command palette' })
    expect(within(within(dialog).getByRole('group', { name: 'Pages' })).getAllByRole('option').map((option) => option.textContent)).toEqual(['Account settings'])
    expect(within(within(dialog).getByRole('group', { name: 'Organizations' })).getAllByRole('option').map((option) => option.textContent)).toEqual(['Acme'])
    expect(within(dialog).queryByRole('group', { name: 'Sites' })).toBeNull()
  })

  it('loads the palette code only when it is first opened', async () => {
    renderShell('/app/acme/docs/settings/general')
    await screen.findByRole('banner', { name: 'Application header' })
    expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument()
    expect(document.querySelector('[cmdk-root]')).toBeNull()
  })
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && bun run test src/components/AppShell.test.tsx src/components/AppHeader.test.tsx src/lib/shortcuts.test.tsx`
Expected: FAIL. There is no "Open command palette" button. `shortcuts.current['mod+k']` is undefined. The help list lacks the palette line. `isApplePlatform` is not exported.

- [ ] **Step 3: Add the strings**

`web/src/messages/en.ts`: after `    sidebar: 'Ctrl/⌘ B — Collapse or expand the sidebar',` add `    palette: 'Ctrl/⌘ K — Open the command palette',`.
`web/src/messages/locales/de.ts`: after `    sidebar: 'Strg/⌘ B — Seitenleiste ein- oder ausklappen',` add `    palette: 'Strg/⌘ K — Befehlspalette öffnen',`.
`web/src/messages/locales/es.ts`: after `    sidebar: 'Ctrl/⌘ B — Contraer o expandir la barra lateral',` add `    palette: 'Ctrl/⌘ K — Abrir la paleta de comandos',`.

`web/src/messages/ui.ts`: replace `  palette: {\n    title: 'Command palette',` with:

```ts
  palette: {
    open: 'Open command palette',
    keyHint: 'Ctrl K',
    keyHintApple: '⌘K',
    title: 'Command palette',
```

`web/src/messages/locales/de.ui.ts`: replace `  palette: {\n    title: 'Befehlspalette',` with:

```ts
  palette: {
    open: 'Befehlspalette öffnen',
    keyHint: 'Strg K',
    keyHintApple: '⌘K',
    title: 'Befehlspalette',
```

`web/src/messages/locales/es.ui.ts`: replace `  palette: {\n    title: 'Paleta de comandos',` with:

```ts
  palette: {
    open: 'Abrir la paleta de comandos',
    keyHint: 'Ctrl K',
    keyHintApple: '⌘K',
    title: 'Paleta de comandos',
```

- [ ] **Step 4: Add `isApplePlatform` to `web/src/lib/shortcuts.ts`**

Append:

```ts

/** Whether to show ⌘ rather than Ctrl in shortcut hints. */
export function isApplePlatform(
  nav: Pick<Navigator, 'platform' | 'userAgent'> | undefined = typeof navigator === 'undefined' ? undefined : navigator,
): boolean {
  if (!nav) return false
  return /Mac|iPhone|iPad|iPod/i.test(nav.platform || nav.userAgent)
}
```

- [ ] **Step 5: Add the header trigger**

`web/src/components/AppHeader.tsx`:
1. Change the lucide import to `import { CircleHelpIcon, MoonIcon, SearchIcon, SunIcon } from 'lucide-react'` and add `import { isApplePlatform } from '@/lib/shortcuts'` after the `isAnonymousTracking` import.
2. Add `  onOpenPalette,` to the destructured props after `  onOpenHelp,`, and `  onOpenPalette: () => void` to the props type after `  onOpenHelp: () => void`.
3. Make the trigger the first child of `<div className="flex items-center gap-1">`, before the live badge:

```tsx
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="gap-1.5 text-muted-foreground"
          aria-label={ui.palette.open}
          aria-keyshortcuts="Control+K Meta+K"
          onClick={onOpenPalette}
        >
          <SearchIcon aria-hidden="true" />
          <kbd className="hidden rounded border bg-muted px-1 font-sans text-[0.7rem] font-medium md:inline">
            {isApplePlatform() ? ui.palette.keyHintApple : ui.palette.keyHint}
          </kbd>
        </Button>
```

4. Change the doc comment's first line to `The single 44px sticky bar above every page: sidebar trigger, breadcrumbs, command palette trigger, live badge, help and theme.`

- [ ] **Step 6: Wire the palette into `AppShell`**

In `web/src/components/AppShell.tsx`:
1. Replace `import { useEffect, useRef, useState } from 'react'` with `import { lazy, Suspense, useEffect, useRef, useState } from 'react'`, and `import { Outlet, useRouterState } from '@tanstack/react-router'` with `import { Outlet, useRouter, useRouterState } from '@tanstack/react-router'`.
2. After `import { privateRequest } from '@/features/settings/privateApi'`, add:

```tsx
import { requestLogout } from '@/lib/session-lifecycle'
import { paletteCommands, type PaletteCommand } from './paletteCommands'

// cmdk and the palette load on first open, so they stay out of the initial JS budget.
const CommandPalette = lazy(() => import('./CommandPalette').then((module) => ({ default: module.CommandPalette })))
```

3. After `const [helpOpen, setHelpOpen] = useState(false)`, add:

```tsx
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [paletteLoaded, setPaletteLoaded] = useState(false)
  const router = useRouter()
```

4. Replace the `useShortcuts({ ... })` call with:

```tsx
  const openPalette = () => {
    setPaletteLoaded(true)
    setPaletteOpen(true)
  }
  const runPaletteCommand = (command: PaletteCommand) => {
    setPaletteOpen(false)
    if (command.kind === 'navigate') router.history.push(command.href)
    else if (command.action === 'theme') toggle()
    else if (command.action === 'help') setHelpOpen(true)
    else void requestLogout()
  }

  useShortcuts({
    d: toggle,
    '?': () => setHelpOpen(true),
    'mod+k': () => {
      setPaletteLoaded(true)
      setPaletteOpen((open) => !open)
    },
  })
```

5. On `<AppHeader ... />`, add the prop `onOpenPalette={openPalette}` after `onOpenHelp={() => setHelpOpen(true)}`.
6. In the help dialog list, insert `<li>{en.shortcuts.palette}</li>` between `<li>{en.shortcuts.sidebar}</li>` and `<li>{en.shortcuts.period}</li>`.
7. Between the help `</Dialog>` and `<Toaster />`, insert:

```tsx
      {paletteLoaded ? (
        <Suspense fallback={null}>
          <CommandPalette
            open={paletteOpen}
            onOpenChange={setPaletteOpen}
            commands={paletteOpen ? paletteCommands({ me, membership, requestedSite, theme }) : []}
            onRun={runPaletteCommand}
          />
        </Suspense>
      ) : null}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd web && bun run test src/components src/lib src/messages src/routes && bun run typecheck`
Expected: all PASS (`AppShell`, `AppHeader`, `shortcuts`, `locales`, `-app-shell-scope`, `-$org`), and the typecheck exits 0.

- [ ] **Step 8: Edit the e2e specs (edited, not run)**

`web/e2e/navigation.spec.ts`: append:

```ts
test('opens the command palette with Ctrl+K and from the header, and navigates without a reload', async ({ page, owner }) => {
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}`
  await page.setViewportSize({ width: 1440, height: 900 })
  await owner.login(page, dashboard)
  let documentRequests = 0
  page.on('request', (request) => {
    if (request.resourceType() === 'document') documentRequests += 1
  })
  await page.keyboard.press('Control+k')
  const palette = page.getByRole('dialog', { name: 'Command palette' })
  await expect(palette).toBeVisible()
  await palette.getByRole('combobox', { name: 'Search commands' }).fill('Funnels')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(`${dashboard}/funnels`)
  await expect(palette).toHaveCount(0)
  expect(documentRequests).toBe(0)

  const trigger = page.getByRole('banner', { name: 'Application header' }).getByRole('button', { name: 'Open command palette' })
  await trigger.click()
  await expect(palette).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(palette).toHaveCount(0)
  await expect(trigger).toBeFocused()
})
```

`web/e2e/a11y-audit.spec.ts`: in `reports axe findings for the requested pages`, after `  reports.push(await audit(page, 'dashboard'))`, insert:

```ts
  await page.getByRole('button', { name: 'Open command palette' }).click()
  await expect(page.getByRole('dialog', { name: 'Command palette' })).toBeVisible()
  reports.push(await audit(page, 'command-palette'))
  await page.keyboard.press('Escape')
```

Do not run Playwright.

- [ ] **Step 9: Group B gate and budget**

Run: `cd web && bun run typecheck && bun run test 2>&1 | tail -8 && bun run build 2>&1 | tail -3`
Expected:
- The typecheck exits 0.
- Tests: the only failure is the baseline `packages/tokens` test.
- The build ends with `Initial JavaScript: N / 256000 bytes gzipped`, N ≤ 256000. Record N.

Then confirm the palette is a lazy chunk:

```bash
cd web && bun -e "const m = JSON.parse(require('fs').readFileSync('dist/.vite/manifest.json', 'utf8')); const c = Object.values(m).find((x) => x.src === 'src/components/CommandPalette.tsx'); console.log(Boolean(c && c.isDynamicEntry), c && c.file)"
```

Expected: `true assets/CommandPalette-<hash>.js`. If it prints `false`, something imports `CommandPalette` or `ui/command` statically. Fix that import before committing.

- [ ] **Step 10: Commit**

```bash
git add web/src/components/AppShell.tsx web/src/components/AppShell.test.tsx web/src/components/AppHeader.tsx web/src/components/AppHeader.test.tsx web/src/lib/shortcuts.ts web/src/lib/shortcuts.test.tsx web/src/messages/en.ts web/src/messages/locales/de.ts web/src/messages/locales/es.ts web/src/messages/ui.ts web/src/messages/locales/de.ui.ts web/src/messages/locales/es.ui.ts web/e2e/navigation.spec.ts web/e2e/a11y-audit.spec.ts
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'
git commit -m "feat(web): open the command palette from the header and Ctrl/Cmd K" -m "Initial JavaScript: N / 256000 bytes gzipped; the palette and cmdk load in a dynamic chunk. E2e specs navigation and a11y-audit edited, not run (the e2e webServer cannot log in to the DB here)."
```

Replace `N` with the measured number. **Group B ends here (spec step 8: one PR).**

---

## Self-review notes (done at plan time)

- **Spec coverage.**
  - Section 2 item 4 and section 9 "every `/app/...` route inside one shell, including account and admin": Tasks 3-4, with route tests for account, admin, no-org, onboarding and not-found.
  - "Exactly one sticky bar, 44 px": the sticky-offsets guard (Task 3), the landmarks guard (Task 4) and `expectShell`.
  - "Breadcrumbs correct on ... account": Task 1 and the Task 3 route test.
  - "Project/org switching exists in exactly one place": the account sidebar reuses `OrganizationMenuGroup` inside the same switcher position (Task 2). The palette's Sites and Organizations groups are shortcuts to the same destinations, not a second switcher UI. See open question 2.
  - Section 3 "⌘K opens the palette", and "the help dialog lists every registered key": Tasks 5 and 8.
  - Section 7 `CommandPalette` on shadcn `command`, with `cmdk` the only new package: Task 7.
  - Section 10 "the palette is the only global search; no decorative search box earlier": the trigger lands only in Task 8.
  - Section 8 regressions: single `main` (Tasks 3-4); sticky offsets (Task 3); last-site recording (unchanged effect, existing AppShell test); anonymous blocking (unchanged boundary plus the Task 6 and 8 palette tests); the `sidebar_state` cookie (`SidebarProvider` stays mounted across scopes, Task 3 persistence test).
- **Placeholders.** None. `N` in the two commit bodies is the measured build number, filled in at execution time.
- **Type consistency.** `AppShell({ me, membership? })`, `AppHeader` props (`membership?`, `onOpenPalette`), `buildCrumbs(pathname, membership?)`, `authorizationScope(userId, membership | undefined)`, `accountDestinations(me)`, `PaletteCommand` and `CommandPalette({ open, onOpenChange, commands, onRun })` match across Tasks 1-8.

## Open questions (not resolvable from the spec or code)

1. **Per-organization Settings dropdown on account pages.** `AccountFrame`'s header `HeaderMenus` listed "Organization settings" for every membership. With one shell, account pages show the account sidebar instead. Org settings are then reached by switching organization (the switcher or the palette) and using the sidebar Settings group. The `HeaderMenus` component is deleted as dead code. Should the account sidebar or palette list "Organization settings" per membership instead?
2. **No-org switcher label.** The account-scope switcher button reads "Choose organization" (new string). The alternative is to hide the switcher entirely in account scope and rely on the brand link and the palette.
3. **Account-page footer.** "Powered by Supermetry" (`AccountFrame` footer) is replaced by the shell footer (DB-IP credit). Should the shell footer show both?
4. **Palette scope.** v2 plan section 5.2 also lists date ranges, density, "create goal", "copy tracking snippet", docs search and `g`-prefixed keys. They are excluded here as non-existent actions or separate features. Confirm they stay deferred.
5. **Onboarding card position.** It is no longer vertically centred in the viewport. It sits near the top of `main`, with no viewport math. Is that acceptable, or should `main` become a flex column so the card can fill and centre?
