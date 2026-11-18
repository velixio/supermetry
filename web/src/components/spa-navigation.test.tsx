import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { Me, Membership } from '@/lib/me'

import { SupermetrySidebar } from './Sidebar'
import { RoutePending } from './RoutePending'
import { SidebarProvider } from './ui/sidebar'
import { TooltipProvider } from './ui/tooltip'

afterEach(() => { cleanup(); localStorage.clear() })

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  effective_capabilities: ['profiles.read', 'replay.read'],
  sites: [{ id: 1, name: 'Docs', public_id: 'docs-public', tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }],
}

const me: Me = {
  user: { id: 1, name: 'Owner', email: 'owner@supermetry.test' },
  memberships: [membership],
}

function renderSidebar(
  initialEntry = '/app/acme/docs-public/realtime',
  options: { membership?: Membership; me?: Me; defaultOpen?: boolean; seed?: boolean | Record<string, boolean> } = {},
) {
  // Groups start collapsed except the active one; most tests want every group open.
  const seed = options.seed ?? true
  localStorage.clear()
  if (seed) localStorage.setItem('sm.sidebar.v1.1', JSON.stringify({ open: seed === true ? { analyze: true, behaviour: true, conversion: true, manage: true } : seed, pinned: [] }))
  const sidebarMembership = options.membership ?? membership
  const sidebarMe = options.me ?? { ...me, memberships: [sidebarMembership] }
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={new QueryClient()}>
      <TooltipProvider>
        <SidebarProvider defaultOpen={options.defaultOpen ?? true}>
          <SupermetrySidebar me={sidebarMe} membership={sidebarMembership} requestedSite="docs-public" />
          <Outlet />
        </SidebarProvider>
      </TooltipProvider>
      </QueryClientProvider>
    ),
  })
  const appRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: 'app',
    component: Outlet,
  })
  const orgSettingsRoute = createRoute({
    getParentRoute: () => appRoute,
    path: '$org/settings',
    component: () => <h1>Org settings page</h1>,
  })
  const siteRoute = createRoute({
    getParentRoute: () => appRoute,
    path: '$org/$site',
    component: () => <Outlet />,
  })
  const siteIndexRoute = createRoute({
    getParentRoute: () => siteRoute,
    path: '/',
    component: () => <h1>Dashboard page</h1>,
  })
  const siteSectionRoute = createRoute({
    getParentRoute: () => siteRoute,
    path: '$section',
    component: () => <h1>Section page</h1>,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([
      appRoute.addChildren([orgSettingsRoute, siteRoute.addChildren([siteIndexRoute, siteSectionRoute])]),
    ]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })

  render(<RouterProvider router={router} />)
  return router
}

describe('SPA navigation', () => {
  beforeEach(() => {
    window.scrollTo = () => undefined
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: () => ({
        matches: false,
        addEventListener: () => undefined,
        removeEventListener: () => undefined,
      }),
    })
  })

  it('uses the client router for sidebar links and exposes the active route', async () => {
    const router = renderSidebar()

    const realtime = await screen.findByRole('link', { name: 'Realtime' })
    expect(realtime).toHaveAttribute('aria-current', 'page')

    fireEvent.click(screen.getByRole('link', { name: 'Dashboard' }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/app/acme/docs-public'))
    expect(screen.getByRole('heading', { name: 'Dashboard page' })).toBeInTheDocument()
  })

  it('places permission-gated Replays immediately after Realtime without a Map destination', async () => {
    renderSidebar()

    const realtime = await screen.findByRole('link', { name: 'Realtime' })
    const replays = screen.getByRole('link', { name: 'Replays' })
    expect(realtime.closest('li')?.nextElementSibling).toBe(replays.closest('li'))
    expect(screen.queryByRole('link', { name: 'Map' })).not.toBeInTheDocument()
  })

  it('groups navigation into labelled 44px-tall sections without changing item order', async () => {
    renderSidebar()

    const expected = {
      Analyze: ['Dashboard', 'Events', 'Sessions', 'Realtime', 'Replays'],
      Behaviour: ['Journeys', 'Retention', 'Profiles', 'Cohorts', 'Heatmaps', 'Web Vitals'],
      Conversion: ['Funnels', 'Goals', 'Revenue'],
      Manage: ['Sites', 'Links and pixels', 'Data health', 'GitHub stars'],
    }
    for (const [name, links] of Object.entries(expected)) {
      const navigation = await screen.findByRole('navigation', { name })
      expect(Array.from(navigation.querySelectorAll('a')).map((link) => link.textContent?.trim())).toEqual(links)
      for (const link of navigation.querySelectorAll('a')) expect(link).toHaveClass('h-11')
    }
  })

  it('shows the brand name and one dropdown listing projects, then organizations', async () => {
    renderSidebar()

    expect(await screen.findByText('Supermetry')).toBeInTheDocument()
    const trigger = screen.getByRole('button', { name: /Docs\s*Acme/ })
    fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false, pointerType: 'mouse' })

    const menu = await screen.findByRole('menu')
    const text = menu.textContent ?? ''
    expect(text.indexOf('Projects')).toBeGreaterThanOrEqual(0)
    expect(text.indexOf('Projects')).toBeLessThan(text.indexOf('Docs'))
    expect(text.indexOf('Docs')).toBeLessThan(text.indexOf('Organizations'))
    expect(text.indexOf('Organizations')).toBeLessThan(text.lastIndexOf('Acme'))
    expect(screen.getByRole('menuitem', { name: /Add organization/ })).toHaveAttribute('href', '/app/account/organizations/new')
  })

  it('renders a stable skeleton fallback for delayed route chunks', () => {
    render(<RoutePending />)

    expect(screen.getByRole('status', { name: 'Loading page' })).toHaveAttribute(
      'aria-busy',
      'true',
    )
    expect(document.querySelectorAll('[data-slot="skeleton"]')).not.toHaveLength(0)
  })

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

  it('shows the GitHub stars link inside an organization to every member', async () => {
    renderSidebar('/app/acme/docs-public')
    const link = await screen.findByRole('link', { name: 'GitHub stars' })
    expect(link).toHaveAttribute('href', '/app/acme/github-stars')
    // It sits in the Manage section with Sites and Health, not in a group of its own.
    const manage = link.closest('nav') as HTMLElement
    expect(within(manage).getByRole('link', { name: 'Sites' })).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: 'Community' })).toBeNull()
    cleanup()
    renderSidebar('/app/acme/docs-public', { me: { ...me, memberships: [{ ...me.memberships[0], role: 'viewer' }] } })
    await screen.findByRole('link', { name: 'Realtime' })
    expect(screen.getByRole('link', { name: 'GitHub stars' })).toHaveAttribute('href', '/app/acme/github-stars')
  })

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
      ['GitHub stars', '/app/acme/github-stars'],
    ])
    fireEvent.pointerDown(screen.getByRole('button', { name: /Docs\s*Acme/ }), { button: 0, ctrlKey: false, pointerType: 'mouse' })
    const menu = await screen.findByRole('menu')
    // The site switcher keeps the current page (Realtime) for the chosen site.
    expect(within(menu).getAllByRole('menuitem').map((item) => item.getAttribute('href'))).toEqual([
      `${site}/realtime`,
      '/app/acme/sites/new',
      `${site}?${base}`,
      '/app/account/organizations/new',
    ])
  })
})

describe('sidebar groups, pinning and scope', () => {
  beforeEach(() => {
    window.scrollTo = () => undefined
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
    })
  })
  const groupButton = (name: string) => screen.getByRole('button', { name })
  const stored = () => JSON.parse(localStorage.getItem('sm.sidebar.v1.1') ?? '{}')

  it('opens only the group with the active route and collapses the rest', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { seed: false })
    await screen.findByRole('link', { name: 'Realtime' })
    expect(groupButton('Analyze')).toHaveAttribute('aria-expanded', 'true')
    for (const name of ['Behaviour', 'Conversion', 'Manage']) {
      expect(groupButton(name)).toHaveAttribute('aria-expanded', 'false')
      expect(groupButton(name)).toHaveAttribute('aria-controls')
    }
    expect(screen.queryByRole('link', { name: 'Journeys' })).toBeNull()
    expect(screen.getByRole('link', { name: 'Journeys', hidden: true })).toBeInTheDocument()
  })

  it('opens a collapsed group by click, Enter or Space, and remembers it per user', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { seed: false })
    await screen.findByRole('link', { name: 'Realtime' })
    fireEvent.click(groupButton('Behaviour'))
    expect(await screen.findByRole('link', { name: 'Journeys' })).toBeInTheDocument()
    expect(stored().open.behaviour).toBe(true)
    // A native button toggles on Enter and Space through click; check the semantics the keys rely on.
    const conversion = groupButton('Conversion')
    expect(conversion.tagName).toBe('BUTTON')
    expect(conversion).toHaveAttribute('type', 'button')
    fireEvent.click(conversion)
    expect(conversion).toHaveAttribute('aria-expanded', 'true')
    fireEvent.click(conversion)
    expect(conversion).toHaveAttribute('aria-expanded', 'false')
    expect(stored().open.conversion).toBe(false)
    cleanup()
    // A fresh mount restores the remembered state from storage.
    localStorage.setItem('sm.sidebar.v1.1', JSON.stringify({ open: { behaviour: true, analyze: false }, pinned: [] }))
    const router = renderSidebar('/app/acme/docs-public/realtime', { seed: { behaviour: true } })
    await screen.findByRole('link', { name: 'Journeys' })
    expect(router).toBeDefined()
  })

  it('works when localStorage throws', async () => {
    const getItem = Storage.prototype.getItem
    const setItem = Storage.prototype.setItem
    Storage.prototype.getItem = function (key: string) { if (key.startsWith('sm.')) throw new Error('blocked'); return getItem.call(this, key) }
    Storage.prototype.setItem = function (key: string, value: string) { if (key.startsWith('sm.')) throw new Error('blocked'); setItem.call(this, key, value) }
    try {
      renderSidebar('/app/acme/docs-public/realtime', { seed: false })
      await screen.findByRole('link', { name: 'Realtime' })
      fireEvent.click(groupButton('Behaviour'))
      expect(groupButton('Behaviour')).toHaveAttribute('aria-expanded', 'true')
    } finally {
      Storage.prototype.getItem = getItem
      Storage.prototype.setItem = setItem
    }
  })

  it('opens the group of the active route when the route changes into a collapsed one', async () => {
    const router = renderSidebar('/app/acme/docs-public/realtime', { seed: { behaviour: false } })
    await screen.findByRole('link', { name: 'Realtime' })
    await router.navigate({ to: '/app/acme/docs-public/journeys' as never })
    await waitFor(() => expect(groupButton('Behaviour')).toHaveAttribute('aria-expanded', 'true'))
  })

  it('pins an item into a Pinned group at the top and unpins it again', async () => {
    renderSidebar('/app/acme/docs-public/realtime')
    await screen.findByRole('link', { name: 'Realtime' })
    fireEvent.click(screen.getByRole('button', { name: 'Pin Funnels to the top' }))
    const pinned = await screen.findByRole('navigation', { name: 'Pinned' })
    expect(within(pinned).getByRole('link', { name: 'Funnels' })).toBeInTheDocument()
    expect(screen.getAllByRole('link', { name: 'Funnels' })).toHaveLength(1)
    expect(stored().pinned).toEqual(['nav:/funnels'])
    const nav = screen.getAllByRole('navigation')
    expect(nav.findIndex((item) => item === pinned)).toBeLessThan(nav.findIndex((item) => item === screen.getByRole('navigation', { name: 'Analyze' })))
    fireEvent.click(screen.getByRole('button', { name: 'Unpin Funnels' }))
    await waitFor(() => expect(screen.queryByRole('navigation', { name: 'Pinned' })).toBeNull())
    expect(within(screen.getByRole('navigation', { name: 'Conversion' })).getByRole('link', { name: 'Funnels' })).toBeInTheDocument()
  })

  it('shows the organization in the switcher, with the site dimmed, on org pages', async () => {
    renderSidebar('/app/acme/settings')
    const trigger = await screen.findByRole('button', { name: /Organization: Acme/ })
    expect(trigger.querySelector('[data-scope="org"]')).not.toBeNull()
    expect(within(trigger).getByTestId('switcher-site-dimmed')).toHaveClass('text-muted-foreground')
    cleanup()
    renderSidebar('/app/acme/docs-public')
    const siteTrigger = await screen.findByRole('button', { name: /Docs\s*Acme/ })
    expect(siteTrigger.querySelector('[data-scope="site"]')).not.toBeNull()
  })

  it('gives the switcher header a solid background and a bottom border so groups never show through', async () => {
    renderSidebar('/app/acme/docs-public')
    await screen.findByRole('link', { name: 'Realtime' })
    const header = document.querySelector('[data-slot="sidebar-header"]')
    expect(header).toHaveClass('sticky', 'top-0', 'bg-sidebar', 'border-b')
  })

  it('puts the plan, language and theme in the account menu, not as a floating pill', async () => {
    renderSidebar('/app/acme/docs-public')
    fireEvent.keyDown(await screen.findByRole('button', { name: 'Account' }), { key: 'ArrowDown' })
    expect(await screen.findByRole('menuitem', { name: 'Language' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: /theme/ })).toBeInTheDocument()
    expect(document.querySelector('[data-account-plan-slot]')).not.toBeNull()
  })
})

describe('icon rail', () => {
  beforeEach(() => {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
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
    expect(screen.getByRole('link', { name: 'Funnels' })).toBeInTheDocument()
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
    expect(await screen.findByRole('button', { name: /Docs\s*Acme/ })).toHaveClass('group-data-[collapsible=icon]:p-0!')
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
