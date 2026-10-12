import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { useLiveSummary, useReplayAvailability, privateRequest } = vi.hoisted(() => ({ useLiveSummary: vi.fn(), useReplayAvailability: vi.fn(), privateRequest: vi.fn() }))

vi.mock('@/features/realtime/useLiveSummary', () => ({ useLiveSummary }))
vi.mock('@/components/replay/api', () => ({
  useReplayAvailability,
}))
vi.mock('@/lib/shortcuts', () => ({ useShortcuts: () => undefined }))
vi.mock('@/lib/theme', () => ({ useTheme: () => ({ theme: 'light', toggle: vi.fn() }) }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import type { Me, Membership } from '@/lib/me'

import { AppShell } from './AppShell'

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  sites: [{ id: 8, name: 'Other', slug: 'other' }, { id: 7, name: 'Docs', slug: 'docs', public_id: 'docs-public', tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }],
}

const me: Me = {
  user: { id: 1, name: 'Owner', email: 'owner@supermetry.test' },
  memberships: [membership],
}

function renderShell(initialEntry: string, shellMembership = membership) {
  const root = createRootRoute({ component: Outlet })
  const app = createRoute({
    getParentRoute: () => root,
    path: 'app',
    component: Outlet,
  })
  const org = createRoute({
    getParentRoute: () => app,
    path: '$org',
    component: () => <AppShell me={{ ...me, memberships: [shellMembership] }} membership={shellMembership} />,
  })
  const site = createRoute({ getParentRoute: () => org, path: '$site', component: Outlet })
  const siteSettings = createRoute({ getParentRoute: () => site, path: 'settings', component: Outlet })
  const siteGeneral = createRoute({
    getParentRoute: () => siteSettings,
    path: 'general',
    component: () => <h1>Site settings</h1>,
  })
  const realtime = createRoute({
    getParentRoute: () => site,
    path: 'realtime',
    component: () => <h1>Realtime page</h1>,
  })
  const orgSettings = createRoute({ getParentRoute: () => org, path: 'settings', component: Outlet })
  const orgMembers = createRoute({
    getParentRoute: () => orgSettings,
    path: 'members',
    component: () => <h1>Organization members</h1>,
  })
  const router = createRouter({
    routeTree: root.addChildren([
      app.addChildren([org.addChildren([
        site.addChildren([siteSettings.addChildren([siteGeneral]), realtime]),
        orgSettings.addChildren([orgMembers]),
      ])]),
    ]),
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

function setWidth(width: number) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: width })
}

describe('AppShell live visitor badge', () => {
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
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    })
    useReplayAvailability.mockReturnValue({ enabled: false })
    useLiveSummary.mockReturnValue({
      data: { active_visitors: 12, visitors_last_30m: 48 },
      isPending: false,
      isError: false,
    })
    privateRequest.mockResolvedValue({})
    setWidth(1440)
    document.cookie = 'sidebar_state=; path=/; max-age=0'
  })

  afterEach(() => {
    document.cookie = 'sidebar_state=; path=/; max-age=0'
    cleanup()
    vi.clearAllMocks()
  })

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

  it('shows counts, accessible detail, tooltip, and a realtime link on nested site routes', async () => {
    const { router } = renderShell('/app/acme/docs-public/settings/general')

    const badge = await screen.findByRole('link', { name: /12 live/ })
    expect(badge).toHaveTextContent('● 12 live')
    expect(badge).toHaveAccessibleDescription('12 distinct visitors with an accepted pageview or custom event in the last 5 minutes. Privacy signals, bots, and site exclusions are not counted. 48 visitors in the last 30 minutes.')
    expect(badge).toHaveAttribute('href', '/app/acme/docs-public/realtime')

    fireEvent.focus(badge)
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      '12 distinct visitors with an accepted pageview or custom event in the last 5 minutes. Privacy signals, bots, and site exclusions are not counted. 48 visitors in the last 30 minutes.',
    )

    fireEvent.click(badge)
    await waitFor(() => expect(router.state.location.pathname).toBe('/app/acme/docs-public/realtime'))
  })

  it('shows breadcrumbs in the header instead of a repeated site name', async () => {
    renderShell('/app/acme/docs/settings/general')
    const nav = await screen.findByRole('navigation', { name: 'Breadcrumb' })
    expect(within(nav).getByRole('link', { name: 'Acme' })).toBeInTheDocument()
    expect(within(nav).getByRole('link', { name: 'Docs' })).toBeInTheDocument()
    expect(within(nav).getByText('General')).toHaveAttribute('aria-current', 'page')
  })

  it('names the site crumb from a public id in the URL', async () => {
    renderShell('/app/acme/docs-public/settings/general')
    const nav = await screen.findByRole('navigation', { name: 'Breadcrumb' })
    expect(within(nav).getByRole('link', { name: 'Docs' })).toBeInTheDocument()
  })

  it('remembers a valid scoped site as the last visited site', async () => {
    renderShell('/app/acme/docs-public/settings/general')
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PATCH', '/api/private/me', {
      body: { preferences: { last_site_id: 7 } },
    }))
  })

  it('owns one labelled main landmark after a first-focusable skip link', async () => {
    const { container } = renderShell('/app/acme/docs-public/settings/general')

    const skip = await screen.findByRole('link', { name: 'Skip to main content' })
    expect(container.querySelector('a,button,input,select,textarea,[tabindex]:not([tabindex="-1"])')).toBe(skip)
    expect(skip).toHaveAttribute('href', '#main-content')
    expect(skip).toHaveClass('-left-[10000px]', 'focus:fixed', 'focus:z-[100]', 'focus:rounded-full', 'focus:ring-2')
    expect(container.querySelectorAll('main')).toHaveLength(1)
    expect(screen.getByRole('main', { name: 'Main content' })).toHaveAttribute('id', 'main-content')
    expect(screen.getByRole('main', { name: 'Main content' })).toHaveAttribute('tabindex', '-1')
    expect(screen.getByRole('banner', { name: 'Application header' })).toBeVisible()
    expect(screen.getByRole('complementary', { name: 'Application sidebar' })).toBeVisible()
  })

  it('credits DB-IP throughout the authenticated app', async () => {
    renderShell('/app/acme/docs-public/settings/general')

    expect(await screen.findByRole('link', { name: 'IP geolocation by DB-IP.com' })).toHaveAttribute(
      'href',
      'https://db-ip.com',
    )
  })

  it.each(['docs', 'docs-public', '7'])('keeps current site context for the %s alias', async (alias) => {
    renderShell(`/app/acme/${alias}/settings/general`)
    expect(await screen.findByRole('link', { name: /12 live/ })).toHaveAttribute('href', `/app/acme/${alias}/realtime`)
    expect(useLiveSummary).toHaveBeenCalledWith(7)
    const settings = screen.getByRole('navigation', { name: 'Settings' })
    expect(within(settings).getByRole('link', { name: 'Site settings' })).toHaveAttribute('href', '/app/acme/docs/settings/general')
  })

  it('keeps settings and account out of the header and offers them in the sidebar', async () => {
    renderShell('/app/acme/docs/settings/general')
    const header = await screen.findByRole('banner', { name: 'Application header' })
    expect(within(header).queryByRole('button', { name: 'Settings' })).not.toBeInTheDocument()
    expect(within(header).queryByRole('button', { name: 'Account' })).not.toBeInTheDocument()
    const sidebar = screen.getByRole('complementary', { name: 'Application sidebar' })
    expect(within(sidebar).getByRole('navigation', { name: 'Settings' })).toBeInTheDocument()
    expect(within(sidebar).getByRole('button', { name: 'Account' })).toBeInTheDocument()
  })

  it('uses the same live label for one visitor', async () => {
    useLiveSummary.mockReturnValue({
      data: { active_visitors: 1, visitors_last_30m: 9 },
      isPending: false,
      isError: false,
    })
    renderShell('/app/acme/docs-public/settings/general')

    expect(await screen.findByRole('link', { name: /1 live/ })).toHaveTextContent('● 1 live')
  })

  it('shows a muted placeholder while loading and hides errors', async () => {
    useLiveSummary.mockReturnValue({ data: undefined, isPending: true, isError: false })
    const { unmount } = renderShell('/app/acme/docs-public/settings/general')
    expect((await screen.findByText('— live')).closest('output')).toHaveClass('text-muted-foreground')

    unmount()
    useLiveSummary.mockReturnValue({ data: undefined, isPending: false, isError: true })
    renderShell('/app/acme/docs-public/settings/general')
    expect(screen.queryByText(/live$/)).not.toBeInTheDocument()
  })

  it.each([
    { name: 'enabled replay with grant', enabled: true, grant: true, visible: true },
    { name: 'disabled replay with grant', enabled: false, grant: true, visible: true },
    { name: 'enabled replay without grant', enabled: true, grant: false, visible: false },
  ])('preserves identified navigation for $name even when project profiles are off', async ({ enabled, grant, visible }) => {
    useReplayAvailability.mockReturnValue({ enabled })
    const identifiedMembership = { ...membership, effective_capabilities: grant ? ['replay.read'] : [] }
    renderShell('/app/acme/docs-public/settings/general', identifiedMembership)
    await screen.findByRole('heading', { name: 'Site settings' })
    const link = screen.queryByRole('link', { name: 'Replays' })
    if (visible) {
      expect(link).toBeVisible()
      expect(new URL(link!.getAttribute('href')!, window.location.origin).pathname).toBe('/app/acme/docs/replays')
    } else {
      expect(link).not.toBeInTheDocument()
    }
    expect(useReplayAvailability).not.toHaveBeenCalled()
  })

  it('blocks a direct anonymous route before its outlet mounts and keeps only live counts', async () => {
    const anonymousMembership: Membership = { ...membership, sites: membership.sites.map((site) => ({ ...site, tracking_policy: { instance_mode: 'anonymous', effective_mode: 'anonymous', configured_profiles_enabled: true, restrictions: ['instance_anonymous'] } })) }
    renderShell('/app/acme/docs-public/realtime', anonymousMembership)
    expect(await screen.findByRole('heading', { name: 'Unavailable in anonymous tracking mode' })).toBeVisible()
    expect(screen.queryByRole('heading', { name: 'Realtime page' })).not.toBeInTheDocument()
    const badge = screen.getByText('12 live').closest('[tabindex="0"]')
    expect(badge).toHaveTextContent('● 12 live')
    expect(badge).toHaveAccessibleDescription('12 distinct visitors with an accepted pageview or custom event in the last 5 minutes. Privacy signals, bots, and site exclusions are not counted. 48 visitors in the last 30 minutes.')
    expect(screen.queryByRole('link', { name: /12 live/ })).not.toBeInTheDocument()
    expect(useReplayAvailability).not.toHaveBeenCalled()
    for (const label of ['Events', 'Sessions', 'Profiles', 'Replays', 'Journeys', 'Retention', 'Heatmaps', 'Web Vitals', 'Funnels', 'Realtime', 'Goals']) {
      expect(screen.queryByRole('link', { name: label })).not.toBeInTheDocument()
    }
  })

  it('does not show the badge on organization-only routes', async () => {
    renderShell('/app/acme/settings/members')

    expect(await screen.findByRole('heading', { name: 'Organization members' })).toBeVisible()
    expect(screen.queryByText(/live$/)).not.toBeInTheDocument()
    expect(useLiveSummary).not.toHaveBeenCalled()
  })

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
})
