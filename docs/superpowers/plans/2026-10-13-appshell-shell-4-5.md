# AppShell Header, Switcher and Rail Implementation Plan (steps 4-5)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Leave the app with one 44 px sticky header, one project and organisation switcher (the sidebar header), Settings and Account in the sidebar, and a sidebar that collapses to an icon rail and starts collapsed on tablets.

**Architecture:** The header block in `AppShell` moves into a new `AppHeader` component, and `ProjectTabs` is deleted. `HeaderMenus` gets a reusable `AccountMenu` export with a replaceable trigger, and the sidebar footer opens it. A Settings group of plain links sits at the bottom of the sidebar content. The sidebar's start state comes from a pure `sidebarStartsOpen(cookie, width)`, where the cookie wins. The repeated default analytics search object becomes one `defaultSearch()` helper in `lib/search.ts`.

**Tech Stack:** React 19, TypeScript, TanStack Router and Query, shadcn/ui (Radix) sidebar, Tailwind v4 with tailwind-merge, Vitest and Testing Library, Playwright, Bun.

**Spec:** `docs/superpowers/specs/2026-10-13-appshell-dashboard-design.md`, sections 2-4 and 6-10. This plan covers section 8 steps 4 and 5 only. Steps 1-3 (`PageContainer`, `PageHeader`, `Breadcrumbs`, `routeCrumbs`, `matchesSite` in `web/src/lib/matchSite.ts`) are done and on this branch. Out of scope: the dashboard filter row, the inspector, the command palette and moving `AccountFrame` onto the shell. Those are steps 6-8 and section 11, each with its own plan.

## Global Constraints

- Scope is `web/` only. No backend change and no new endpoint.
- Use Bun: `bun install --frozen-lockfile`, `bun run <script>`, `bunx`. Never use npm.
- `bun run build` enforces the JS budget of 256 KB gzip (`scripts/size-check.mjs`). No new package in this plan.
- No new hardcoded English. Strings go in `web/src/messages/en.ts` or `web/src/messages/ui.ts`, plus the same keys in `web/src/messages/locales/{de,es}.ts` or `{de,es}.ui.ts`. `web/src/messages/locales/locales.test.ts` enforces parity. Read strings at render time, never at module scope.
- Use existing tokens and no new colours. Use lucide icons, 16 px in nav and header.
- Exactly one `main` landmark with `id="main-content"`. The skip link stays the first focusable element. Tab order is skip link, sidebar, header, main.
- Keep the privacy behaviour: `AnalyticsPrivacyBoundary` stays around the `Outlet`, and anonymous-tracking sites get no links to blocked analytics screens (`visibleNavigation` and `anonymousRouteRestricted` are unchanged).
- Keep the last-site recording (`PATCH /api/private/me` in `AppShell`) and the `sidebar_state` cookie, which shadcn `ui/sidebar.tsx` writes on every toggle. Do not edit `web/src/components/ui/sidebar.tsx`.
- The header is exactly 44 px (`h-11`), `sticky top-0`, and is the only sticky bar above the content.
- Responsive: at 1280 px and wider the sidebar starts expanded. From 768 to 1279 px it starts on the icon rail, but a `sidebar_state` cookie wins when one is set. Below 768 px it is the existing sheet (`useIsMobile`).
- Commit identity is `velixio <270455167+velixio@users.noreply.github.com>`. Commit messages describe the change only: no trailers, no tool or assistant names.
- Before every commit, scan the added lines for email addresses: `git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'`. Stop and ask the human about any match other than the noreply address above.
- Playwright cannot run in the execution environment, because the e2e webServer fails on DB login. Pin every behaviour with Vitest and Testing Library. The e2e edits listed in the tasks must still be made, but they cannot be run here. Say so in each PR description, and do not claim they pass.
- Work in two PR-sized groups, each on its own branch and created in order from the current tip. Branches: `feat/appshell-shell-4-header` (Tasks 1-4), then `feat/appshell-shell-5-rail` (Tasks 5-7), branched from the tip of the first. Never push to main, and do not push unless the user asks.

## Review Focus

- **Viewer role.** The sidebar Settings group shows only "Organization settings", never "Site settings", and the switcher has no "Add site". Pinned in Task 2 (role table) and Task 3 (switcher).
- **Organisation with zero sites.** The header still sits at `top-0`, the Settings group shows only "Organization settings", and the switcher (labelled "Site") still offers "Add site" to owners. Pinned in Task 3.
- **Anonymous-tracking site.** Adding the Settings group must not give an anonymous site any analytics link, and the Site settings target (`/settings/general`) is not a restricted path. Pinned in Task 2 (`anonymousRouteRestricted` check), and the existing AppShell anonymous test still passes.
- **Site addressed by an alias (public id or numeric id).** The Site settings link still points at the canonical slug and is marked active. Pinned in Task 2 (`settingsIsActive` alias case) and the existing AppShell alias test.
- **Tablet width with a saved or malformed cookie.** `sidebar_state=true` or `false` wins over width in both directions, and garbage values fall back to width. A short viewport on the icon rail must scroll rather than clip the bottom items. Pinned in Task 5 (cookie table) and Task 6 (`overflow-auto` on the rail).

---

## File Structure

Line numbers refer to the code at plan time (commit `3931a7c6`). Earlier tasks shift them, so find each spot by the quoted code.

Create:
- `web/src/components/AppHeader.tsx`: the 44 px sticky header (trigger, breadcrumbs, live badge, help, theme). `SiteLiveBadge` moves here.
- `web/src/components/sidebarState.ts`: `SIDEBAR_EXPANDED_MIN_WIDTH` and pure `sidebarStartsOpen(cookie, width)`.
- `web/src/components/sidebarState.test.ts`
- `web/src/components/sticky-offsets.test.ts`: a source guard listing every sticky `top-*` offset.

Modify:
- `web/src/lib/matchSite.ts`: gains `siteSlug` (moved from `Sidebar.tsx` to break a `Sidebar` ↔ `HeaderMenus` import cycle).
- `web/src/components/HeaderMenus.tsx`: export `AccountMenu`. `HeaderMenus` composes it; `AccountFrame` still uses `HeaderMenus` unchanged.
- `web/src/components/Sidebar.tsx`: Settings group, footer account menu, `settingsDestinations`, `settingsIsActive`, `defaultSearch()` adoption, rail fixes.
- `web/src/components/AppShell.tsx`: use `AppHeader`, drop `ProjectTabs` and `HeaderMenus`, use `sidebarStartsOpen`, complete the help dialog.
- `web/src/lib/search.ts`: `defaultSearch()`.
- `web/src/components/replay/ReplayNavLink.tsx`, `web/src/features/sites/SitesOverview.tsx`, `web/src/features/demo/useDemo.ts`: adopt `defaultSearch()`.
- `web/src/features/settings/SettingsPages.tsx:734`: the sticky aside offset clears the header.
- `web/src/messages/en.ts`, `web/src/messages/locales/de.ts`, `web/src/messages/locales/es.ts`: three shortcut strings.
- Tests: `AppShell.test.tsx`, `HeaderMenus.test.tsx`, `spa-navigation.test.tsx`, `sidebar-navigation.test.tsx`, `sidebar-height.test.tsx`, `web/src/lib/search.test.ts`.
- E2e (edit only, cannot run here): `web/e2e/account-navigation.spec.ts`, `web/e2e/navigation.spec.ts`, `web/e2e/a11y-audit.spec.ts`.

Delete:
- `web/src/components/ProjectTabs.tsx`. It has no unit test of its own; its behaviour, the site links and "Add site", is already in the sidebar switcher and is pinned by Task 3's switcher test.

E2e specs checked and left unchanged, because nothing they select moves: `privacy-navigation.spec.ts` (opens the sheet with "Toggle sidebar" and checks only analytics link names), `authorization.spec.ts` (its "Account settings" link is in the org settings page body, `routes/app.$org.settings.index.tsx:39`), `ui-qa.spec.ts`, `not-found.spec.ts` and `sites-overview.spec.ts` (theme button stays in the header).

Baseline, recorded before Task 1 (output: 7 files, 71 tests passed on 2026-10-13):

```bash
cd web && bun install --frozen-lockfile && bun run typecheck && bun run test 2>&1 | tail -15
```

---

## Group A: header and switcher consolidation (branch `feat/appshell-shell-4-header`)

```bash
git switch -c feat/appshell-shell-4-header
```

### Task 1: Reusable account menu

**Files:**
- Modify: `web/src/lib/matchSite.ts`, `web/src/components/HeaderMenus.tsx` (whole file), `web/src/components/Sidebar.tsx:50`, `:102-104`, `:133`
- Test: `web/src/components/HeaderMenus.test.tsx`

**Interfaces:**
- Produces:
  - `siteSlug(site: Site): string` exported from `@/lib/matchSite`, and still re-exported from `./Sidebar`.
  - `AccountMenu(props: { me: Me; trigger?: ReactNode; side?: 'top' | 'right' | 'bottom' | 'left'; align?: 'start' | 'center' | 'end' })` exported from `./HeaderMenus`. Without `trigger` it renders today's ghost "Account" button. The menu content is unchanged: identity label, version, Account settings, Instance administration (instance admins only), Log out.
  - `HeaderMenus` keeps its props and output.

- [ ] **Step 1: Write the failing test**

Append to `web/src/components/HeaderMenus.test.tsx`, and change the import on line 6 to `import { AccountMenu, HeaderMenus } from './HeaderMenus'`:

```tsx
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

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && bun run test -- src/components/HeaderMenus.test.tsx`
Expected: FAIL in the new test ("Element type is invalid", because `AccountMenu` is undefined). Every existing test passes.

- [ ] **Step 3: Move `siteSlug` into `lib/matchSite.ts`**

Append to `web/src/lib/matchSite.ts`:

```ts
/** The canonical URL segment for a site: slug, then public id, then numeric id. */
export function siteSlug(site: Site): string {
  return site.slug ?? site.public_id ?? String(site.id)
}
```

In `web/src/components/Sidebar.tsx`, make three edits:
- Line 50: `import { matchesSite } from '@/lib/matchSite'` becomes `import { matchesSite, siteSlug } from '@/lib/matchSite'`.
- Delete the local `export function siteSlug(site: Site): string { ... }` at lines 102-104.
- Line 133: `export { matchesSite }` becomes `export { matchesSite, siteSlug }`.

- [ ] **Step 4: Rewrite `HeaderMenus.tsx`**

Replace the whole of `web/src/components/HeaderMenus.tsx` with:

```tsx
import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { en } from '@/messages/en'
import { Building2Icon, LogOutIcon, SettingsIcon, ShieldCheckIcon, UserRoundIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem,
  DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { requestLogout } from '@/lib/session-lifecycle'
import { siteSlug } from '@/lib/matchSite'
import type { Me, Membership, Site } from '@/lib/me'
import { ui } from '@/messages/ui'

/**
 * Identity, build version, account settings, instance administration and log out.
 * The trigger is replaceable so the sidebar footer opens the same menu.
 */
export function AccountMenu({
  me,
  trigger,
  side,
  align = 'end',
}: {
  me: Me
  trigger?: ReactNode
  side?: 'top' | 'right' | 'bottom' | 'left'
  align?: 'start' | 'center' | 'end'
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        {trigger ?? <Button variant="ghost" size="sm"><UserRoundIcon aria-hidden="true" />{ui.menus.account}</Button>}
      </DropdownMenuTrigger>
      <DropdownMenuContent side={side} align={align} className="w-72 max-w-[calc(100vw-2rem)]" aria-label={ui.menus.accountMenu}>
        <DropdownMenuLabel className="flex flex-col gap-1">
          <span className="truncate">{me.user.name || ui.menus.yourAccount}</span>
          <span className="break-all text-xs font-normal text-muted-foreground">{me.user.email}</span>
          <span className="text-xs font-normal text-muted-foreground">Supermetry {me.version || 'dev'}</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          <DropdownMenuItem asChild><Link to="/app/account/profile"><UserRoundIcon aria-hidden="true" />{ui.menus.accountSettings}</Link></DropdownMenuItem>
          {me.user.instance_admin ? <DropdownMenuItem asChild><Link to="/app/admin/users"><ShieldCheckIcon aria-hidden="true" />{en.admin.link}</Link></DropdownMenuItem> : null}
          <DropdownMenuItem onSelect={() => void requestLogout()}><LogOutIcon aria-hidden="true" />{ui.menus.logOut}</DropdownMenuItem>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function HeaderMenus({ me, membership, site }: { me: Me; membership?: Membership; site?: Site }) {
  const organizations = membership ? [membership] : me.memberships
  return (
    <div className="flex shrink-0 items-center gap-1">
      {organizations.length > 0 ? (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="sm"><SettingsIcon aria-hidden="true" />{ui.menus.settings}</Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-72 max-w-[calc(100vw-2rem)]" aria-label={ui.menus.settingsMenu}>
            <DropdownMenuGroup>
              {organizations.map((item) => (
                <DropdownMenuItem asChild key={item.org.id}>
                  <Link to="/app/$org/settings" params={{ org: item.org.slug }}>
                    <Building2Icon aria-hidden="true" />
                    <span className="min-w-0"><span className="block">{ui.menus.organizationSettings}</span><span className="block truncate text-xs text-muted-foreground">{item.org.name}</span></span>
                  </Link>
                </DropdownMenuItem>
              ))}
              {membership && site && membership.role !== 'viewer' ? (
                <DropdownMenuItem asChild>
                  <Link to="/app/$org/$site/settings/general" params={{ org: membership.org.slug, site: siteSlug(site) }}>
                    <SettingsIcon aria-hidden="true" />
                    <span className="min-w-0"><span className="block">{ui.menus.siteSettings}</span><span className="block truncate text-xs text-muted-foreground">{site.name}</span></span>
                  </Link>
                </DropdownMenuItem>
              ) : null}
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
      <AccountMenu me={me} />
    </div>
  )
}
```

The `Supermetry {me.version || 'dev'}` text was already there and is kept exactly as it is. It is a brand name, not new copy.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd web && bun run test -- src/components/HeaderMenus.test.tsx src/components/AppShell.test.tsx src/components/spa-navigation.test.tsx && bun run typecheck`
Expected: all PASS, typecheck clean.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/matchSite.ts web/src/components/HeaderMenus.tsx web/src/components/HeaderMenus.test.tsx web/src/components/Sidebar.tsx
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'   # expect no output
git commit -m "refactor(web): make the account menu reusable with a custom trigger"
```

### Task 2: Settings group and account footer in the sidebar; header menus removed

**Files:**
- Modify: `web/src/components/Sidebar.tsx` (imports at lines 4-22, 38-49, 52-54; new exports after `activeSite` at line 137; `SupermetrySidebar` body at lines 226 and 318-362)
- Modify: `web/src/components/AppShell.tsx:32` (import) and `:166` (`<HeaderMenus ... />`)
- Test: `web/src/components/sidebar-navigation.test.tsx`, `web/src/components/spa-navigation.test.tsx`, `web/src/components/AppShell.test.tsx:183-184`
- E2e edit (cannot run here): `web/e2e/account-navigation.spec.ts`

**Interfaces:**
- Consumes: `AccountMenu` and `siteSlug` from Task 1. `matchesSite(site, requested)` from `@/lib/matchSite`.
- Produces:
  - `type SettingsDestination = 'site' | 'org'`
  - `settingsDestinations(role: Role, site: Site | undefined): SettingsDestination[]` returns `['site', 'org']` when a site exists and the role is not `viewer`, otherwise `['org']`.
  - `settingsIsActive(destination: SettingsDestination, pathname: string, org: string, site?: Site): boolean`
  - The sidebar renders a `nav` labelled by heading "Settings" (`id="sidebar-settings-heading"`) after the Manage group, and a `SidebarFooter` with a `SidebarMenuButton` named "Account" that opens `AccountMenu`.

- [ ] **Step 1: Write the failing pure-function tests**

In `web/src/components/sidebar-navigation.test.tsx`, change the import line to:

```ts
import { anonymousRouteRestricted } from './AnalyticsPrivacyBoundary'
import { settingsDestinations, settingsIsActive, visibleNavigation } from './Sidebar'
```

and add inside the `describe` block:

```ts
  it('offers site settings to every role but viewers, and organisation settings to all', () => {
    const site: Site = { id: 7, name: 'Docs', slug: 'docs' }
    for (const role of ['owner', 'admin', 'editor'] as const) expect(settingsDestinations(role, site)).toEqual(['site', 'org'])
    expect(settingsDestinations('viewer', site)).toEqual(['org'])
    expect(settingsDestinations('owner', undefined)).toEqual(['org'])
  })

  it('marks the settings destination active for its own section, by any site alias', () => {
    const site: Site = { id: 7, name: 'Docs', slug: 'docs', public_id: 'docs-public' }
    expect(settingsIsActive('org', '/app/acme/settings/members', 'acme', site)).toBe(true)
    expect(settingsIsActive('org', '/app/acme/docs/settings/general', 'acme', site)).toBe(false)
    expect(settingsIsActive('org', '/app/other/settings', 'acme', site)).toBe(false)
    for (const alias of ['docs', 'docs-public', '7']) {
      expect(settingsIsActive('site', `/app/acme/${alias}/settings/general`, 'acme', site)).toBe(true)
    }
    expect(settingsIsActive('site', '/app/acme/docs', 'acme', site)).toBe(false)
    expect(settingsIsActive('site', '/app/acme/settings', 'acme', site)).toBe(false)
    expect(settingsIsActive('site', '/app/acme/docs/settings/general', 'acme', undefined)).toBe(false)
  })

  it('links site settings to a page that anonymous tracking does not block', () => {
    expect(anonymousRouteRestricted('/app/acme/docs/settings/general')).toBe(false)
  })
```

- [ ] **Step 2: Write the failing render tests**

In `web/src/components/spa-navigation.test.tsx`:

1. Change line 1 to `import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'`.
2. Replace the `renderSidebar` signature and its root component (lines 33-43) with:

```tsx
function renderSidebar(
  initialEntry = '/app/acme/docs-public/realtime',
  options: { membership?: Membership; me?: Me; defaultOpen?: boolean } = {},
) {
  const sidebarMembership = options.membership ?? membership
  const sidebarMe = options.me ?? { ...me, memberships: [sidebarMembership] }
  const rootRoute = createRootRoute({
    component: () => (
      <TooltipProvider>
        <SidebarProvider defaultOpen={options.defaultOpen ?? true}>
          <SupermetrySidebar me={sidebarMe} membership={sidebarMembership} requestedSite="docs-public" />
          <Outlet />
        </SidebarProvider>
      </TooltipProvider>
    ),
  })
```

The rest of `renderSidebar` stays as it is. It still returns `router`, so existing callers keep working.

3. Add inside `describe('SPA navigation', ...)`:

```tsx
  it.each([
    ['owner', ['Site settings', 'Organization settings']],
    ['admin', ['Site settings', 'Organization settings']],
    ['editor', ['Site settings', 'Organization settings']],
    ['viewer', ['Organization settings']],
  ] as const)('lists settings for a %s in a group after Manage', async (role, labels) => {
    renderSidebar('/app/acme/docs-public', { membership: { ...membership, role } })
    const settings = await screen.findByRole('navigation', { name: 'Settings' })
    expect(Array.from(settings.querySelectorAll('a')).map((link) => link.textContent?.trim())).toEqual(labels)
    expect(within(settings).getByRole('link', { name: 'Organization settings' })).toHaveAttribute('href', '/app/acme/settings')
    if (role !== 'viewer') {
      expect(within(settings).getByRole('link', { name: 'Site settings' })).toHaveAttribute('href', '/app/acme/docs-public/settings/general')
    }
    const manage = screen.getByRole('navigation', { name: 'Manage' })
    expect(manage.compareDocumentPosition(settings) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    for (const link of settings.querySelectorAll('a')) expect(link).toHaveClass('h-11')
  })

  it('marks site settings as the current page on a settings route', async () => {
    renderSidebar('/app/acme/docs-public/settings')
    const settings = await screen.findByRole('navigation', { name: 'Settings' })
    expect(within(settings).getByRole('link', { name: 'Site settings' })).toHaveAttribute('aria-current', 'page')
    expect(within(settings).getByRole('link', { name: 'Organization settings' })).not.toHaveAttribute('aria-current')
  })

  it('opens the account menu from the sidebar footer, with instance administration for admins', async () => {
    renderSidebar('/app/acme/docs-public', { me: { ...me, user: { ...me.user, instance_admin: true } } })
    const account = await screen.findByRole('button', { name: 'Account' })
    expect(account.closest('[data-slot="sidebar-footer"]')).not.toBeNull()
    fireEvent.keyDown(account, { key: 'ArrowDown' })
    expect(await screen.findByRole('menuitem', { name: 'Account settings' })).toHaveAttribute('href', '/app/account/profile')
    expect(screen.getByRole('menuitem', { name: 'Instance administration' })).toHaveAttribute('href', '/app/admin/users')
    expect(screen.getByRole('menuitem', { name: 'Log out' })).toBeInTheDocument()
  })
```

The `'/app/acme/docs-public/settings'` entry matches the existing `$section` route in this test's tree, so the page renders "Section page".

4. In `web/src/components/AppShell.test.tsx`, replace lines 183-184 (the header Settings menu check inside the alias `it.each`) with:

```tsx
    const settings = screen.getByRole('navigation', { name: 'Settings' })
    expect(within(settings).getByRole('link', { name: 'Site settings' })).toHaveAttribute('href', '/app/acme/docs/settings/general')
```

and add a test inside the `describe`:

```tsx
  it('keeps settings and account out of the header and offers them in the sidebar', async () => {
    renderShell('/app/acme/docs/settings/general')
    const header = await screen.findByRole('banner', { name: 'Application header' })
    expect(within(header).queryByRole('button', { name: 'Settings' })).not.toBeInTheDocument()
    expect(within(header).queryByRole('button', { name: 'Account' })).not.toBeInTheDocument()
    const sidebar = screen.getByRole('complementary', { name: 'Application sidebar' })
    expect(within(sidebar).getByRole('navigation', { name: 'Settings' })).toBeInTheDocument()
    expect(within(sidebar).getByRole('button', { name: 'Account' })).toBeInTheDocument()
  })
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd web && bun run test -- src/components/sidebar-navigation.test.tsx src/components/spa-navigation.test.tsx src/components/AppShell.test.tsx`
Expected: FAIL. `settingsDestinations is not a function`, no navigation named "Settings", and a header still holding the Settings and Account buttons. The `anonymousRouteRestricted` test passes already; it is a guard.

- [ ] **Step 4: Implement in `Sidebar.tsx`**

Add `Building2Icon`, `SettingsIcon` and `UserRoundIcon` to the lucide import (`UserRoundIcon` is already imported at line 20; add only the two missing names). Add `SidebarFooter` to the `@/components/ui/sidebar` import (lines 38-49). After line 54, add:

```tsx
import { AccountMenu } from './HeaderMenus'
```

After `activeSite` (line 137), add:

```tsx
export type SettingsDestination = 'site' | 'org'

/** Settings links at the bottom of the sidebar. Site settings need a site and a role above viewer. */
export function settingsDestinations(role: Role, site: Site | undefined): SettingsDestination[] {
  return site && role !== 'viewer' ? ['site', 'org'] : ['org']
}

export function settingsIsActive(destination: SettingsDestination, pathname: string, org: string, site?: Site): boolean {
  const [, app, pathOrg, scope, section] = pathname.split('/')
  if (app !== 'app' || pathOrg !== org) return false
  if (destination === 'org') return scope === 'settings'
  return site !== undefined && matchesSite(site, scope) && section === 'settings'
}
```

In `SupermetrySidebar`, change line 226 to:

```tsx
  const { setOpenMobile, isMobile } = useSidebar()
```

Insert this immediately before `</SidebarContent>` (line 361), after the `navigationSections.map(...)` block:

```tsx
        <SidebarGroup className="mt-auto">
          <SidebarGroupLabel asChild><h2 id="sidebar-settings-heading">{ui.menus.settings}</h2></SidebarGroupLabel>
          <SidebarGroupContent>
            <nav aria-labelledby="sidebar-settings-heading">
              <SidebarMenu>
                {settingsDestinations(membership.role, site).map((destination) => {
                  const active = settingsIsActive(destination, pathname, membership.org.slug, site)
                  const label = destination === 'site' ? ui.menus.siteSettings : ui.menus.organizationSettings
                  return (
                    <SidebarMenuItem key={destination}>
                      <SidebarMenuButton asChild className="h-11" isActive={active} tooltip={label}>
                        {destination === 'site' && site ? (
                          <Link
                            aria-current={active ? 'page' : undefined}
                            onClick={closeMobile}
                            params={{ org: membership.org.slug, site: siteSlug(site) }}
                            to="/app/$org/$site/settings/general"
                          >
                            <SettingsIcon />
                            <span>{label}</span>
                          </Link>
                        ) : (
                          <Link
                            aria-current={active ? 'page' : undefined}
                            onClick={closeMobile}
                            params={{ org: membership.org.slug }}
                            to="/app/$org/settings"
                          >
                            <Building2Icon />
                            <span>{label}</span>
                          </Link>
                        )}
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  )
                })}
              </SidebarMenu>
            </nav>
          </SidebarGroupContent>
        </SidebarGroup>
```

Insert this immediately after `</SidebarContent>` and before `</Sidebar>`:

```tsx
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
```

The footer button keeps the visible label "Account", the same text as today's header button. That keeps the accessible name and the e2e `login` helper's selector stable, and the menu itself shows the user's name.

- [ ] **Step 5: Remove the header menus from `AppShell.tsx`**

Delete line 32 (`import { HeaderMenus } from './HeaderMenus'`) and line 166 (`<HeaderMenus me={me} membership={membership} site={scopedSite} />`).

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web && bun run test -- src/components src/features/demo src/routes && bun run typecheck`
Expected: all PASS. If a test outside this task fails only because it now finds a second "Account" button or a new "Settings" navigation, scope that query to its container (`within(...)`). Do not loosen any other assertion.

- [ ] **Step 7: Edit the e2e spec (cannot be run here)**

In `web/e2e/account-navigation.spec.ts`, replace the first test (lines 31-60) with:

```ts
test('sidebar settings links and account menu work on desktop and mobile with keyboard focus', async ({ page, owner }) => {
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}`)
  const header = page.getByRole('banner', { name: 'Application header' })
  await expect(header.getByRole('button', { name: 'Settings', exact: true })).toHaveCount(0)
  await expect(header.getByRole('button', { name: 'Account', exact: true })).toHaveCount(0)
  for (const width of [1280, 375]) {
    await page.setViewportSize({ width, height: 800 })
    if (width < 768) await page.getByRole('button', { name: 'Toggle sidebar' }).click()
    const sidebar = width < 768
      ? page.getByRole('dialog', { name: 'Sidebar' })
      : page.getByRole('complementary', { name: 'Application sidebar' })
    const settings = sidebar.getByRole('navigation', { name: 'Settings', exact: true })
    await expect(settings.getByRole('link', { name: 'Organization settings', exact: true })).toHaveAttribute('href', `/app/${owner.orgSlug}/settings`)
    await expect(settings.getByRole('link', { name: 'Site settings', exact: true })).toHaveAttribute('href', `/app/${owner.orgSlug}/${owner.siteSlug}/settings/general`)
    const account = sidebar.getByRole('button', { name: 'Account', exact: true })
    await expect(account).toBeInViewport()
    await account.focus()
    await page.keyboard.press('ArrowDown')
    await expect(page.getByText(owner.email, { exact: true }).last()).toBeVisible()
    await expect(page.getByRole('menuitem', { name: 'Log out', exact: true })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(account).toBeFocused()
    if (width < 768) await page.keyboard.press('Escape')
  }
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.getByRole('button', { name: 'Account', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Account settings', exact: true }).click()
  await expect(page).toHaveURL('/app/account/profile')
  await expect(page.getByRole('navigation', { name: 'Account settings' })).toBeVisible()
  await page.getByRole('link', { name: 'Security', exact: true }).click()
  await expect(page).toHaveURL('/app/account/security')
  await page.screenshot({ path: 'e2e/screenshots/account-mobile.png', fullPage: true })
})
```

In the per-role test (lines 64-73), replace the four lines from `await page.getByRole('button', { name: 'Settings', exact: true }).click()` through `await page.keyboard.press('Escape')` with:

```ts
    const settings = page.getByRole('navigation', { name: 'Settings', exact: true })
    await expect(settings.getByRole('link', { name: 'Organization settings', exact: true })).toBeVisible()
    await expect(settings.getByRole('link', { name: 'Site settings', exact: true })).toHaveCount(role === 'viewer' ? 0 : 1)
```

In the no-site test, replace lines 79-81 (open Settings, expect no Site settings, press Escape) with:

```ts
  const settings = page.getByRole('navigation', { name: 'Settings', exact: true })
  await expect(settings.getByRole('link', { name: 'Organization settings', exact: true })).toBeVisible()
  await expect(settings.getByRole('link', { name: 'Site settings', exact: true })).toHaveCount(0)
```

Leave line 89 (`Settings` button count 0 on the no-organization screen) and every `Account` button selector as they are. On the AppShell the button now lives in the sidebar footer with the same name. On `AccountFrame` pages it is still `HeaderMenus`.

- [ ] **Step 8: Commit**

```bash
git add web/src/components/Sidebar.tsx web/src/components/AppShell.tsx web/src/components/sidebar-navigation.test.tsx web/src/components/spa-navigation.test.tsx web/src/components/AppShell.test.tsx web/e2e/account-navigation.spec.ts
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'   # expect no output
git commit -m "feat(web): move settings and account from the header into the sidebar"
```

### Task 3: AppHeader as the single 44 px sticky bar; ProjectTabs removed

**Files:**
- Create: `web/src/components/AppHeader.tsx`, `web/src/components/sticky-offsets.test.ts`
- Modify: `web/src/components/AppShell.tsx` (imports at lines 1-36, `SiteLiveBadge` at lines 38-72, header block at lines 132-168)
- Modify: `web/src/features/settings/SettingsPages.tsx:734`
- Delete: `web/src/components/ProjectTabs.tsx`
- Test: `web/src/components/AppShell.test.tsx`
- E2e edit (cannot run here): `web/e2e/navigation.spec.ts`, `web/e2e/a11y-audit.spec.ts`

**Interfaces:**
- Consumes: `Breadcrumbs({ membership })` from `./Breadcrumbs`, and `Theme` from `@/lib/theme`.
- Produces: `AppHeader(props: { membership: Membership; site?: Site; requestedSite?: string; theme: Theme; onToggleTheme: () => void; onOpenHelp: () => void })`. Theme state stays in `AppShell`, because `useTheme` keeps local state and the `d` shortcut there must toggle the same instance.

- [ ] **Step 1: Write the failing tests**

Add inside the `describe` in `web/src/components/AppShell.test.tsx`:

```tsx
  it('renders exactly one 44px sticky bar above the content and no project tabs', async () => {
    const { container } = renderShell('/app/acme/docs/settings/general')
    const header = await screen.findByRole('banner', { name: 'Application header' })
    expect(header).toHaveClass('sticky', 'top-0', 'h-11')
    expect(container.querySelectorAll('.sticky')).toHaveLength(1)
    expect(screen.queryByRole('navigation', { name: 'Projects' })).not.toBeInTheDocument()
  })

  it('switches projects and organisations only from the sidebar header', async () => {
    renderShell('/app/acme/docs/settings/general')
    const sidebar = await screen.findByRole('complementary', { name: 'Application sidebar' })
    expect(screen.queryAllByRole('link', { name: 'Other' })).toHaveLength(0)
    expect(screen.queryByRole('link', { name: 'Add project' })).not.toBeInTheDocument()
    fireEvent.pointerDown(within(sidebar).getByRole('button', { name: /Docs\s*Acme/ }), { button: 0, ctrlKey: false, pointerType: 'mouse' })
    const menu = await screen.findByRole('menu')
    expect(within(menu).getByRole('menuitem', { name: /Other/ }).getAttribute('href')).toMatch(/^\/app\/acme\/other\?/)
    expect(within(menu).getByRole('menuitem', { name: /Add site/ })).toHaveAttribute('href', '/app/acme/sites/new')
  })

  it('pins the header to the top with no sites and keeps Add site in the switcher', async () => {
    renderShell('/app/acme/settings/members', { ...membership, sites: [] })
    const header = await screen.findByRole('banner', { name: 'Application header' })
    expect(header).toHaveClass('top-0')
    const settings = screen.getByRole('navigation', { name: 'Settings' })
    expect(within(settings).getAllByRole('link').map((link) => link.textContent)).toEqual(['Organization settings'])
    fireEvent.pointerDown(screen.getByRole('button', { name: /Site\s*Acme/ }), { button: 0, ctrlKey: false, pointerType: 'mouse' })
    expect(await screen.findByRole('menuitem', { name: /Add site/ })).toHaveAttribute('href', '/app/acme/sites/new')
  })

  it('orders focus as skip link, sidebar, header, then main', async () => {
    renderShell('/app/acme/docs/settings/general')
    const skip = await screen.findByRole('link', { name: 'Skip to main content' })
    const sidebar = screen.getByRole('complementary', { name: 'Application sidebar' })
    const header = screen.getByRole('banner', { name: 'Application header' })
    const main = screen.getByRole('main', { name: 'Main content' })
    const follows = (a: Node, b: Node) => Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)
    expect(follows(skip, sidebar)).toBe(true)
    expect(follows(sidebar, header)).toBe(true)
    expect(follows(header, main)).toBe(true)
  })
```

Create `web/src/components/sticky-offsets.test.ts`:

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

// The app header is the only sticky bar above content: 44px (h-11) at top-0.
// Anything that sticks inside a page must clear it; dialogs and AccountFrame have their own scroll context.
const EXPECTED = [
  'src/components/AccountFrame.tsx: sticky top-0',
  'src/components/AppHeader.tsx: sticky top-0',
  'src/components/FullReportDialog.tsx: sticky top-0',
  'src/features/settings/SettingsPages.tsx: lg:sticky lg:top-17',
]

it('lists every sticky top offset and keeps them clear of the 44px app header', () => {
  const files = sourceFiles(join(web, 'src'))
  const found = files.flatMap((file) =>
    [...readFileSync(file, 'utf8').matchAll(/((?:[\w-]+:)?sticky)\b[^"'`\n]*?\s((?:[\w-]+:)?top-[^\s"'`]+)/g)]
      .map((match) => `${relative(web, file)}: ${match[1]} ${match[2]}`),
  )
  expect(found.sort()).toEqual([...EXPECTED].sort())
  for (const file of files) expect(readFileSync(file, 'utf8')).not.toMatch(/\btop-9\b/)
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && bun run test -- src/components/AppShell.test.tsx src/components/sticky-offsets.test.ts`
Expected: FAIL. The header is `h-14`, two `.sticky` elements are found, the `Projects` navigation and an `Other` tab link exist, and the guard lists `ProjectTabs.tsx: sticky top-0` and `SettingsPages.tsx: lg:sticky lg:top-6` and finds `top-9` in `AppShell.tsx`. The focus-order test passes already; it is a guard for the move.

- [ ] **Step 3: Create `AppHeader.tsx`**

```tsx
import { useId } from 'react'
import { Link } from '@tanstack/react-router'
import { CircleHelpIcon, MoonIcon, SunIcon } from 'lucide-react'

import { Breadcrumbs } from './Breadcrumbs'
import { LiveBadge } from '@/components/LiveBadge'
import { Button } from '@/components/ui/button'
import { SidebarTrigger } from '@/components/ui/sidebar'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { useLiveSummary } from '@/features/realtime/useLiveSummary'
import type { Membership, Site } from '@/lib/me'
import type { Theme } from '@/lib/theme'
import { isAnonymousTracking } from '@/lib/tracking-policy'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'

function SiteLiveBadge({ siteId, org, site, detailsAvailable }: { siteId: number; org: string; site: string; detailsAvailable: boolean }) {
  const descriptionId = useId()
  const summary = useLiveSummary(siteId)
  if (summary.isError) return null

  const description = summary.data
    ? en.liveAccuracy.badgeDescription
      .replace('{count}', String(summary.data.active_visitors))
      .replace('{thirty}', String(summary.data.visitors_last_30m))
    : ui.appShell.loadingLive

  const badge = (
    <LiveBadge
      count={summary.data?.active_visitors ?? 0}
      disableLabel={description}
      enableLabel={description}
      enabled
      label={summary.data ? (count) => `${count} live` : () => '— live'}
      muted={summary.isPending}
    />
  )
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        {detailsAvailable ? (
          <Link aria-describedby={descriptionId} params={{ org, site }} to="/app/$org/$site/realtime">
            {badge}
          </Link>
        ) : <span aria-describedby={descriptionId} tabIndex={0}>{badge}</span>}
      </TooltipTrigger>
      <TooltipContent>{description}</TooltipContent>
      <span className="sr-only" id={descriptionId}>{description}</span>
    </Tooltip>
  )
}

/** The single 44px sticky bar above every page: sidebar trigger, breadcrumbs, live badge, help and theme. */
export function AppHeader({
  membership,
  site,
  requestedSite,
  theme,
  onToggleTheme,
  onOpenHelp,
}: {
  membership: Membership
  site?: Site
  requestedSite?: string
  theme: Theme
  onToggleTheme: () => void
  onOpenHelp: () => void
}) {
  return (
    <header aria-label={en.a11y2.applicationHeader} className="sticky top-0 z-20 flex h-11 shrink-0 items-center justify-between gap-2 border-b bg-background px-4">
      <div className="flex min-w-0 items-center gap-2">
        <SidebarTrigger aria-label={en.navigation.collapse} />
        <Breadcrumbs membership={membership} />
      </div>
      <div className="flex items-center gap-1">
        {site && requestedSite ? (
          <div className="hidden sm:block">
            <SiteLiveBadge
              detailsAvailable={!isAnonymousTracking(site)}
              org={membership.org.slug}
              site={requestedSite}
              siteId={site.id}
            />
          </div>
        ) : null}
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className="hidden sm:inline-flex"
          aria-label={en.shortcuts.open}
          onClick={onOpenHelp}
        >
          <CircleHelpIcon />
        </Button>
        <Button
          type="button"
          size="icon"
          variant="ghost"
          aria-label={theme === 'dark' ? en.theme.dark : en.theme.light}
          onClick={onToggleTheme}
        >
          {theme === 'dark' ? <SunIcon /> : <MoonIcon />}
        </Button>
      </div>
    </header>
  )
}
```

`SiteLiveBadge` moves here verbatim, including its two existing `live` label strings. Do not change them in this task.

- [ ] **Step 4: Wire it into `AppShell.tsx` and delete `ProjectTabs`**

1. Delete the `SiteLiveBadge` function (lines 38-72).
2. Replace the import block (lines 1-36, which no longer includes `HeaderMenus` after Task 2) with:

```tsx
import { AuthorizationCacheBoundary, authorizationScope } from '@/lib/authorization-cache'
import { AuthorizationProvider } from '@/lib/authorization'
import { useEffect, useRef, useState } from 'react'
import { Outlet, useRouterState } from '@tanstack/react-router'

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
```

3. Inside `<SidebarInset>`, replace everything from `{membership.sites.length > 0 ? <ProjectTabs` through the closing `</header>` with:

```tsx
          <AppHeader
            membership={membership}
            site={scopedSite}
            requestedSite={requestedSite}
            theme={theme}
            onToggleTheme={toggle}
            onOpenHelp={() => setHelpOpen(true)}
          />
```

4. Run `git rm web/src/components/ProjectTabs.tsx`.

- [ ] **Step 5: Clear the header with the one in-page sticky aside**

In `web/src/features/settings/SettingsPages.tsx:734`, change `lg:sticky lg:top-6` to `lg:sticky lg:top-17`. That is 44 px of header plus the old 24 px gap, which is 4.25rem.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web && bun run test -- src/components src/features/settings && bun run typecheck`
Expected: all PASS, and typecheck reports no unused imports.

- [ ] **Step 7: Edit the e2e specs (cannot be run here)**

In `web/e2e/navigation.spec.ts`, after `await owner.login(page, dashboard)` (line 17), add:

```ts
  await expect(page.getByRole('navigation', { name: 'Projects' })).toHaveCount(0)
  expect((await page.getByRole('banner', { name: 'Application header' }).boundingBox())?.height).toBe(44)
```

In `web/e2e/a11y-audit.spec.ts:159`, extend the heading list to `['Analyze', 'Behaviour', 'Conversion', 'Manage', 'Settings']`. Leave the `- 72` scroll offset at line 179 as it is. It only positions a screenshot, and 72 px still clears the shorter 44 px header.

- [ ] **Step 8: Group verification**

Run, and record the output:

```bash
cd web && bun run typecheck && bun run test 2>&1 | tail -15 && bun run build 2>&1 | tail -8
```

Expected: all PASS, and the JS budget holds. Playwright `navigation`, `account-navigation`, `a11y-audit` and `ui-qa` cannot run here, so list them in the PR as "edited, not run". If a browser is available, take screenshots of the dashboard and site settings at 1440, 1024 and 390 px, and confirm one 44 px bar and no tab strip.

- [ ] **Step 9: Commit**

```bash
git add -A web/src web/e2e
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'   # expect no output
git commit -m "feat(web): extract AppHeader as the single 44px sticky bar and drop project tabs"
```

### Task 4: One `defaultSearch()` helper

**Files:**
- Modify: `web/src/lib/search.ts` (after `dashboardSearch`, line 177)
- Modify: `web/src/components/Sidebar.tsx` (`navigationLink`, lines 139-212; switcher links at lines 258 and 292)
- Modify: `web/src/components/replay/ReplayNavLink.tsx:20`, `web/src/features/sites/SitesOverview.tsx:87,105,132`, `web/src/features/demo/useDemo.ts:22`
- Test: `web/src/lib/search.test.ts`, `web/src/components/spa-navigation.test.tsx`

**Interfaces:**
- Produces: `defaultSearch(): Pick<DashboardSearch, 'period' | 'compare' | 'f' | 'metric' | 'include_imported'>`. It returns a new object and a new `f` array on every call, equal to `parseDashboardSearch({})`.
- Out of scope, because their spelling differs: `AnalyticsPrivacyBoundary.tsx:58` (`include_imported: false`) and `ReplayLink.tsx:29` (`as const` literals).

- [ ] **Step 1: Write a characterisation test for every sidebar link target (expected to PASS before the refactor)**

Add inside `describe('SPA navigation', ...)` in `web/src/components/spa-navigation.test.tsx`:

```tsx
  it('keeps every sidebar and switcher link target unchanged', async () => {
    renderSidebar()
    await screen.findByRole('link', { name: 'Realtime' })
    const base = 'period=30d&compare=off&f=%5B%5D&metric=visitors&include_imported=true'
    const site = '/app/acme/docs-public'
    const links = ['Analyze', 'Behaviour', 'Conversion', 'Manage'].flatMap((name) =>
      Array.from(screen.getByRole('navigation', { name }).querySelectorAll('a')).map((link) => [link.textContent?.trim(), link.getAttribute('href')]),
    )
    expect(links).toEqual([
      ['Dashboard', `${site}?${base}`],
      ['Events', `${site}/events?${base}&name=&property=`],
      ['Sessions', `${site}/sessions?${base}`],
      ['Realtime', `${site}/realtime`],
      ['Replays', `${site}/replays?${base}`],
      ['Journeys', `${site}/journeys?${base}&direction=start&path=&steps=3`],
      ['Retention', `${site}/retention?${base}&interval=day&identity=anonymous`],
      ['Profiles', `${site}/profiles`],
      ['Cohorts', `${site}/cohorts?${base}`],
      ['Heatmaps', `${site}/heatmaps?${base}&path=&device=desktop`],
      ['Web Vitals', `${site}/vitals?${base}`],
      ['Funnels', `${site}/funnels`],
      ['Goals', `${site}/goals`],
      ['Revenue', `${site}/revenue?${base}`],
      ['Sites', '/app/acme/sites'],
      ['Links and pixels', `${site}/links`],
      ['Data health', '/app/acme/health'],
    ])
    fireEvent.pointerDown(screen.getByRole('button', { name: /Docs\s*Acme/ }), { button: 0, ctrlKey: false, pointerType: 'mouse' })
    const menu = await screen.findByRole('menu')
    expect(within(menu).getAllByRole('menuitem').map((item) => item.getAttribute('href'))).toEqual([
      `${site}?${base}`,
      '/app/acme/sites/new',
      `${site}?${base}`,
      '/app/account/organizations/new',
    ])
  })
```

These hrefs were recorded from the current code on 2026-10-13.

- [ ] **Step 2: Write the failing helper tests**

In `web/src/lib/search.test.ts`, add `defaultSearch` and `parseDashboardSearch` to the `./search` import, add these imports at the top:

```ts
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
```

and append:

```ts
describe('defaultSearch', () => {
  it('equals what the dashboard parses from an empty query', () => {
    expect(defaultSearch()).toEqual(parseDashboardSearch({}))
  })

  it('returns a fresh filter array on every call', () => {
    const first = defaultSearch()
    first.f.push('x')
    expect(defaultSearch().f).toEqual([])
  })

  it('is the only place that spells out the default analytics search', () => {
    const web = join(__dirname, '..', '..')
    const literal = ["period: '30d'", "compare: 'off'", 'f: []', "metric: 'visitors'", 'include_imported: true'].join(', ')
    const files = (dir: string): string[] => readdirSync(dir).flatMap((name) => {
      const path = join(dir, name)
      if (statSync(path).isDirectory()) return files(path)
      return /\.tsx?$/.test(name) && !/\.test\./.test(name) ? [path] : []
    })
    const offenders = files(join(web, 'src'))
      .filter((file) => readFileSync(file, 'utf8').includes(literal))
      .map((file) => relative(web, file))
    expect(offenders).toEqual(['src/lib/search.ts'])
  })
})
```

- [ ] **Step 3: Run the tests**

Run: `cd web && bun run test -- src/lib/search.test.ts src/components/spa-navigation.test.tsx`
Expected: the characterisation test PASSES. The `defaultSearch` tests FAIL (`defaultSearch is not a function`, and the offenders list holds `Sidebar.tsx`, `ReplayNavLink.tsx`, `SitesOverview.tsx` and `useDemo.ts`).

- [ ] **Step 4: Implement the helper**

Append after `export const dashboardSearch = ...` in `web/src/lib/search.ts`:

```ts
/** Search params a fresh link into an analytics page starts from: last 30 days, no comparison, no filters. */
export function defaultSearch(): Pick<DashboardSearch, 'period' | 'compare' | 'f' | 'metric' | 'include_imported'> {
  return { period: '30d', compare: 'off', f: [], metric: 'visitors', include_imported: true }
}
```

- [ ] **Step 5: Adopt it**

First, add `import { defaultSearch } from '@/lib/search'` to `Sidebar.tsx`, `ReplayNavLink.tsx`, `SitesOverview.tsx` and `useDemo.ts`.

Then replace every plain occurrence in the four files at once:

```bash
cd web && sed -i "s/{ period: '30d', compare: 'off', f: \[\], metric: 'visitors', include_imported: true }/defaultSearch()/g" \
  src/components/Sidebar.tsx src/components/replay/ReplayNavLink.tsx src/features/sites/SitesOverview.tsx src/features/demo/useDemo.ts
```

This turns `search={{ ... }}` into `search={defaultSearch()}` and `search: { ... },` into `search: defaultSearch(),`. It leaves the four extended objects in `navigationLink` alone. Edit those by hand in `Sidebar.tsx`:

- `/events`: `search={{ ...defaultSearch(), name: '', property: '' }}`
- `/journeys`: `search={{ ...defaultSearch(), direction: 'start', path: '', steps: 3 }}`
- `/retention`: `search={{ ...defaultSearch(), interval: 'day', identity: 'anonymous' }}`
- `/heatmaps`: `search={{ ...defaultSearch(), path: '', device: 'desktop' }}`

Then check that nothing is left: `grep -rn "period: '30d', compare: 'off', f: \[\]" web/src --include=*.ts --include=*.tsx | grep -v test` should list only `AnalyticsPrivacyBoundary.tsx` (`include_imported: false`) and `search.ts`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web && bun run test -- src/lib/search.test.ts src/components src/features/sites src/features/demo && bun run typecheck`
Expected: all PASS, and the characterisation test is unchanged and green.

- [ ] **Step 7: Group verification and commit**

```bash
cd web && bun run typecheck && bun run test 2>&1 | tail -15 && bun run build 2>&1 | tail -8
git add -A web/src
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'   # expect no output
git commit -m "refactor(web): share one default analytics search helper"
```

Open the Group A PR from `feat/appshell-shell-4-header`. The description lists the four commits, the Vitest, typecheck and build output, and says plainly that `account-navigation`, `navigation` and `a11y-audit` were edited but not run, because Playwright cannot log in to the DB here.

---

## Group B: sidebar rail and responsive (branch `feat/appshell-shell-5-rail`)

```bash
git switch -c feat/appshell-shell-5-rail   # from the tip of feat/appshell-shell-4-header
```

### Task 5: Tablet starts on the icon rail; cookie wins

**Files:**
- Create: `web/src/components/sidebarState.ts`, `web/src/components/sidebarState.test.ts`
- Modify: `web/src/components/AppShell.tsx` (the local `sidebarStartsOpen`, formerly lines 74-78, and `useState(sidebarStartsOpen)`, formerly line 89)
- Test: `web/src/components/AppShell.test.tsx`
- E2e edit (cannot run here): `web/e2e/navigation.spec.ts`

**Interfaces:**
- Produces: `SIDEBAR_EXPANDED_MIN_WIDTH = 1280` and `sidebarStartsOpen(cookie: string, width: number): boolean`. The cookie is the raw `document.cookie`, and the width is `window.innerWidth`. Only exactly `sidebar_state=true` or `sidebar_state=false` counts; anything else falls back to width.

- [ ] **Step 1: Write the failing unit test**

Create `web/src/components/sidebarState.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import { SIDEBAR_EXPANDED_MIN_WIDTH, sidebarStartsOpen } from './sidebarState'

describe('sidebarStartsOpen', () => {
  it('expands from 1280px', () => {
    expect(SIDEBAR_EXPANDED_MIN_WIDTH).toBe(1280)
  })

  it.each([
    ['', 1440, true],
    ['', 1280, true],
    ['', 1279, false],
    ['', 1024, false],
    ['', 768, false],
    ['sidebar_state=true', 1024, true],
    ['sidebar_state=false', 1440, false],
    ['theme=dark; sidebar_state=true', 800, true],
    ['sidebar_state=garbage', 1440, true],
    ['sidebar_state=garbage', 1024, false],
    ['other_sidebar_state=false', 1440, true],
  ])('cookie %j at %ipx starts open=%s', (cookie, width, open) => {
    expect(sidebarStartsOpen(cookie, width)).toBe(open)
  })
})
```

- [ ] **Step 2: Write the failing shell test**

In `web/src/components/AppShell.test.tsx`, add above `describe(...)`:

```tsx
function setWidth(width: number) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: width })
}
```

At the end of the existing `beforeEach`, add `setWidth(1440)` and `document.cookie = 'sidebar_state=; path=/; max-age=0'`. This keeps every existing test on the expanded sidebar; jsdom defaults to 1024 px. Add the same cookie-clearing line to `afterEach`. Then add:

```tsx
  it.each([
    [1024, '', 'collapsed'],
    [1279, '', 'collapsed'],
    [1280, '', 'expanded'],
    [1024, 'sidebar_state=true', 'expanded'],
    [1440, 'sidebar_state=false', 'collapsed'],
  ])('at %ipx with cookie %j the sidebar starts %s', async (width, cookie, state) => {
    setWidth(width)
    if (cookie) document.cookie = `${cookie}; path=/`
    const { container } = renderShell('/app/acme/docs/settings/general')
    await screen.findByRole('banner', { name: 'Application header' })
    expect(container.querySelector('[data-slot="sidebar"]')).toHaveAttribute('data-state', state)
  })
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd web && bun run test -- src/components/sidebarState.test.ts src/components/AppShell.test.tsx`
Expected: FAIL. The `./sidebarState` import does not resolve, and the 1024 and 1279 px cases without a cookie start `expanded`.

- [ ] **Step 4: Implement**

Create `web/src/components/sidebarState.ts`:

```ts
/** From this width the desktop sidebar starts expanded; between the mobile sheet (768px) and here it starts on the icon rail. */
export const SIDEBAR_EXPANDED_MIN_WIDTH = 1280

/** The saved `sidebar_state` cookie wins; without one, wide screens start expanded and tablets start on the rail. */
export function sidebarStartsOpen(cookie: string, width: number): boolean {
  const saved = cookie.split('; ').find((entry) => entry.startsWith('sidebar_state='))
  if (saved === 'sidebar_state=true') return true
  if (saved === 'sidebar_state=false') return false
  return width >= SIDEBAR_EXPANDED_MIN_WIDTH
}
```

In `AppShell.tsx`, delete the local `function sidebarStartsOpen(): boolean { ... }`, add `import { sidebarStartsOpen } from './sidebarState'`, and change `const [sidebarOpen] = useState(sidebarStartsOpen)` to:

```tsx
  const [sidebarOpen] = useState(() => sidebarStartsOpen(document.cookie, window.innerWidth))
```

The default is computed once, on mount, like today. Resizing a live window does not re-collapse, and the cookie still records every toggle (`ui/sidebar.tsx:84`).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd web && bun run test -- src/components && bun run typecheck`
Expected: all PASS.

- [ ] **Step 6: Edit the e2e spec (cannot be run here)**

Append to `web/e2e/navigation.spec.ts`:

```ts
test('starts on the icon rail at tablet width and keeps the saved sidebar state', async ({ page, owner }) => {
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}`
  await page.setViewportSize({ width: 1024, height: 800 })
  await owner.login(page, dashboard)
  const sidebar = page.locator('[data-slot="sidebar"]')
  await expect(sidebar).toHaveAttribute('data-state', 'collapsed')
  await expect(page.getByRole('link', { name: 'Funnels' })).toBeVisible()
  await page.getByRole('button', { name: 'Toggle sidebar' }).click()
  await expect(sidebar).toHaveAttribute('data-state', 'expanded')
  await page.reload()
  await expect(sidebar).toHaveAttribute('data-state', 'expanded')
  expect((await page.getByRole('banner', { name: 'Application header' }).boundingBox())?.height).toBe(44)
})
```

- [ ] **Step 7: Commit**

```bash
git add web/src/components/sidebarState.ts web/src/components/sidebarState.test.ts web/src/components/AppShell.tsx web/src/components/AppShell.test.tsx web/e2e/navigation.spec.ts
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'   # expect no output
git commit -m "feat(web): start the sidebar on the icon rail at tablet widths"
```

### Task 6: Icon rail that stays usable

**Files:**
- Modify: `web/src/components/Sidebar.tsx` (switcher `SidebarMenuButton`, formerly line 242; `<SidebarContent>`, formerly line 318)
- Test: `web/src/components/spa-navigation.test.tsx`, `web/src/components/sidebar-height.test.tsx`

**Interfaces:**
- Consumes: `renderSidebar(initialEntry, { defaultOpen })` from Task 2's test helper.
- Produces: no new exports. `SupermetrySidebar` keeps `collapsible="icon"` (`Sidebar.tsx:234`). In rail mode the content scrolls (`group-data-[collapsible=icon]:overflow-auto`), and the switcher's 20 px site icon fits the 32 px rail button (`group-data-[collapsible=icon]:p-1.5!`).
- Deliberately not added: shadcn `SidebarRail`. It carries hardcoded English (`"Toggle Sidebar"`, `ui/sidebar.tsx:285-288`) and is `tabIndex={-1}`. The header trigger and Ctrl/Cmd+B already toggle the rail with a keyboard.

- [ ] **Step 1: Write the failing tests**

Add a new `describe` to `web/src/components/spa-navigation.test.tsx`:

```tsx
describe('icon rail', () => {
  beforeEach(() => {
    window.scrollTo = () => undefined
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
    })
  })
  afterEach(() => {
    document.cookie = 'sidebar_state=; path=/; max-age=0'
  })

  it('collapses to an icon rail that keeps every destination named', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { defaultOpen: false })
    await screen.findByRole('link', { name: 'Realtime' })
    const sidebar = document.querySelector('[data-slot="sidebar"]')
    expect(sidebar).toHaveAttribute('data-state', 'collapsed')
    expect(sidebar).toHaveAttribute('data-collapsible', 'icon')
    for (const name of ['Dashboard', 'Events', 'Sessions', 'Realtime', 'Replays', 'Funnels', 'Site settings', 'Organization settings']) {
      expect(screen.getByRole('link', { name })).toBeInTheDocument()
    }
    expect(screen.getByRole('button', { name: /Docs\s*Acme/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Account' })).toBeInTheDocument()
  })

  it('names a collapsed item with a tooltip on keyboard focus', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { defaultOpen: false })
    fireEvent.focus(await screen.findByRole('link', { name: 'Funnels' }))
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Funnels')
  })

  it('scrolls the rail on short screens instead of clipping the bottom items', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { defaultOpen: false })
    await screen.findByRole('link', { name: 'Realtime' })
    const content = document.querySelector('[data-slot="sidebar-content"]')
    expect(content).toHaveClass('group-data-[collapsible=icon]:overflow-auto')
    expect(content).not.toHaveClass('group-data-[collapsible=icon]:overflow-hidden')
  })

  it('fits the site switcher icon inside the 32px rail button', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { defaultOpen: false })
    expect(await screen.findByRole('button', { name: /Docs\s*Acme/ })).toHaveClass('group-data-[collapsible=icon]:p-1.5!')
  })

  it('toggles with Ctrl or Cmd+B and records the state in the sidebar_state cookie', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { defaultOpen: false })
    await screen.findByRole('link', { name: 'Realtime' })
    fireEvent.keyDown(window, { key: 'b', ctrlKey: true })
    await waitFor(() => expect(document.querySelector('[data-slot="sidebar"]')).toHaveAttribute('data-state', 'expanded'))
    expect(document.cookie).toContain('sidebar_state=true')
    fireEvent.keyDown(window, { key: 'b', metaKey: true })
    await waitFor(() => expect(document.querySelector('[data-slot="sidebar"]')).toHaveAttribute('data-state', 'collapsed'))
    expect(document.cookie).toContain('sidebar_state=false')
  })
})
```

The file's vitest import (line 10) already includes `afterEach`, `beforeEach`, `describe`, `expect` and `it`, and `waitFor` is already imported from Testing Library, so no import change is needed.

Add to `web/src/components/sidebar-height.test.tsx`, inside its `describe`:

```tsx
  it('keeps an icon-width column painted when collapsed to the rail', () => {
    const { container } = render(
      <SidebarProvider defaultOpen={false}>
        <Sidebar collapsible="icon" />
        <main style={{ height: 2000 }} />
      </SidebarProvider>,
    )
    expect(container.querySelector('[data-slot="sidebar"]')).toHaveAttribute('data-collapsible', 'icon')
    expect(container.querySelector('[data-slot="sidebar-gap"]')).toHaveClass('bg-sidebar', 'group-data-[collapsible=icon]:w-(--sidebar-width-icon)')
  })
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && bun run test -- src/components/spa-navigation.test.tsx src/components/sidebar-height.test.tsx`
Expected: FAIL only in "scrolls the rail" (the content still has `overflow-hidden` in icon mode) and "fits the site switcher icon". The named-items, tooltip, Ctrl/Cmd+B and painted-column tests pass already; they pin existing shadcn behaviour against regressions.

- [ ] **Step 3: Implement**

In `web/src/components/Sidebar.tsx`:

- On the switcher, change `<SidebarMenuButton className="h-auto py-1.5" tooltip={...}>` to `<SidebarMenuButton className="h-auto py-1.5 group-data-[collapsible=icon]:p-1.5!" tooltip={...}>`.
- Change `<SidebarContent>` to `<SidebarContent className="group-data-[collapsible=icon]:overflow-auto">`. `cn` (tailwind-merge) drops the primitive's conflicting `group-data-[collapsible=icon]:overflow-hidden`, and the primitive's `no-scrollbar` keeps the rail visually clean.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web && bun run test -- src/components && bun run typecheck`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/Sidebar.tsx web/src/components/spa-navigation.test.tsx web/src/components/sidebar-height.test.tsx
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'   # expect no output
git commit -m "fix(web): keep the collapsed sidebar rail scrollable and its switcher icon in bounds"
```

### Task 7: The help dialog lists every shortcut

**Files:**
- Modify: `web/src/messages/en.ts:255-261`, `web/src/messages/locales/de.ts:257-263`, `web/src/messages/locales/es.ts:257-263`
- Modify: `web/src/components/AppShell.tsx` (the help `<ul>`, formerly lines 197-200)
- Test: `web/src/components/AppShell.test.tsx`

**Interfaces:**
- Consumes: the registered shortcuts are `d` and `?` (`AppShell.tsx`, `useShortcuts`), `m` and `c` (`routes/app.$org.$site.index.tsx:94-101`; `m` sets `period: '30d'` and `c` toggles `compare` between `off` and `previous_period`), and Ctrl/Cmd+B (`ui/sidebar.tsx:31,95-108`). No other global `keydown` listener exists in `web/src`.
- Produces: `en.shortcuts.sidebar`, `en.shortcuts.period` and `en.shortcuts.compare`.

- [ ] **Step 1: Write the failing test**

Add inside the `describe` in `web/src/components/AppShell.test.tsx`:

```tsx
  it('lists every registered keyboard shortcut in the help dialog', async () => {
    renderShell('/app/acme/docs/settings/general')
    fireEvent.click(await screen.findByRole('button', { name: 'Show keyboard shortcuts' }))
    const dialog = await screen.findByRole('dialog', { name: 'Keyboard shortcuts' })
    expect(within(dialog).getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      'D — Toggle dark mode',
      '? — Show this help',
      'Ctrl/⌘ B — Collapse or expand the sidebar',
      'M — Show the last 30 days (dashboard)',
      'C — Turn period comparison on or off (dashboard)',
    ])
  })
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && bun run test -- src/components/AppShell.test.tsx`
Expected: FAIL, because only two list items are found.

- [ ] **Step 3: Add the strings**

In `web/src/messages/en.ts`, inside `shortcuts`, after `help: '? — Show this help',` add:

```ts
    sidebar: 'Ctrl/⌘ B — Collapse or expand the sidebar',
    period: 'M — Show the last 30 days (dashboard)',
    compare: 'C — Turn period comparison on or off (dashboard)',
```

In `web/src/messages/locales/de.ts`, after `help: '? — Diese Hilfe anzeigen',` add:

```ts
    sidebar: 'Strg/⌘ B — Seitenleiste ein- oder ausklappen',
    period: 'M — Letzte 30 Tage anzeigen (Dashboard)',
    compare: 'C — Periodenvergleich ein- oder ausschalten (Dashboard)',
```

In `web/src/messages/locales/es.ts`, after `help: '? — Mostrar esta ayuda',` add:

```ts
    sidebar: 'Ctrl/⌘ B — Contraer o expandir la barra lateral',
    period: 'M — Mostrar los últimos 30 días (panel)',
    compare: 'C — Activar o desactivar la comparación de periodos (panel)',
```

- [ ] **Step 4: Render them**

In `AppShell.tsx`, replace the help list with:

```tsx
          <ul className="flex flex-col gap-2 text-sm">
            <li>{en.shortcuts.theme}</li>
            <li>{en.shortcuts.help}</li>
            <li>{en.shortcuts.sidebar}</li>
            <li>{en.shortcuts.period}</li>
            <li>{en.shortcuts.compare}</li>
          </ul>
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd web && bun run test -- src/components/AppShell.test.tsx src/messages`
Expected: all PASS, including the de/es catalogue parity test.

- [ ] **Step 6: Group verification and commit**

```bash
cd web && bun run typecheck && bun run test 2>&1 | tail -15 && bun run build 2>&1 | tail -8
git add web/src/messages/en.ts web/src/messages/locales/de.ts web/src/messages/locales/es.ts web/src/components/AppShell.tsx web/src/components/AppShell.test.tsx
git diff --cached -U0 | grep '^+' | grep -nE '[[:alnum:]._+-]+@[[:alnum:].-]+'   # expect no output
git commit -m "fix(web): list every keyboard shortcut in the help dialog"
```

Open the Group B PR from `feat/appshell-shell-5-rail`. The description includes the Vitest, typecheck and build output, and states that the new `navigation.spec.ts` tablet test was written but not run. If a browser is available, take screenshots at 1440, 1024 and 390 px in light and dark: expanded at 1440, rail at 1024, sheet at 390, and no horizontal scroll at 390.

---

## Wrap-up

- [ ] Add a dated entry to `progress.md` (steps 4-5 shipped; the e2e edits are unrun pending a DB-capable runner; next is step 6 plus section 11). Record decisions in `memory.md`: the footer button keeps the label "Account", the Settings group is links rather than a menu, the tablet default is computed once on mount, `SidebarRail` was skipped, and `siteSlug` moved to `lib/matchSite.ts`.
- [ ] Leave `AccountFrame` (`h-14` header, its `HeaderMenus`) and `Onboarding`'s `min-h-[calc(100dvh-3.5rem)]` (`features/sites/Onboarding.tsx:31`, sized to AccountFrame's 56 px header) alone. Step 7 moves them onto the shell.

## Self-review (spec coverage)

| Spec requirement | Task |
|---|---|
| Step 4: extract `AppHeader` | 3 |
| Step 4: remove `ProjectTabs`; sidebar header is the single project/org switcher, "Add site" stays inside | 3 (deletion and switcher tests), 2 (no new switcher) |
| Step 4: Settings and Account move to the sidebar, `HeaderMenus` reused not rewritten | 1 (`AccountMenu` extracted from `HeaderMenus`), 2 |
| Section 3: Settings group (Site settings for non-viewers, Org settings); Account and Admin in the footer menu | 2 |
| Section 2.2 and acceptance: one header of 44 px, exactly one sticky bar | 3 (`h-11`, `.sticky` count, sticky-offset guard, `SettingsPages` aside) |
| Section 8 regressions: `top-9` gone, single `main`, last-site recording, anonymous blocking, `sidebar_state` cookie, privacy-navigation | 3 (guard), existing AppShell tests kept green, 6 (cookie), privacy spec unchanged |
| Section 3: one `defaultSearch()` helper, no behaviour change | 4 (characterisation hrefs and source guard) |
| Section 3: Tab order skip link, sidebar, header, main | 3 |
| Step 5: icon-rail collapse (`collapsible="icon"`), cookie kept | 6 |
| Section 6: 768-1279 rail by default, cookie wins; >=1280 expanded; <768 sheet | 5 (sheet path unchanged, `useIsMobile`) |
| Step 5: help dialog lists every registered key | 7 |
| Step 4/5 test updates: `AppShell.test`, `sidebar-navigation.test`, `HeaderMenus.test`, `sidebar-height.test`, rail test, e2e navigation specs | 1, 2, 3, 5, 6 (e2e edited, not runnable here) |
| No new hardcoded English | 2 reuses `ui.menus.*`; 7 adds keys to en, de and es; `ProjectTabs` (hardcoded "Projects"/"Add project") deleted |
| Out of scope: filter row, inspector, palette, AccountFrame move | Not in this plan |
