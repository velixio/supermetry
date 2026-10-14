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
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { toast } from 'sonner'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { loadPalette, useLiveSummary, useUpdateStatus, useReplayAvailability, privateRequest, shortcuts, toggleTheme, requestLogout } = vi.hoisted(() => ({
  loadPalette: vi.fn(),
  useLiveSummary: vi.fn(),
  useUpdateStatus: vi.fn(),
  useReplayAvailability: vi.fn(),
  privateRequest: vi.fn(),
  shortcuts: { current: {} as Record<string, () => void> },
  toggleTheme: vi.fn(),
  requestLogout: vi.fn(),
}))

vi.mock('@/features/realtime/useLiveSummary', () => ({ useLiveSummary }))
vi.mock('@/features/admin/adminApi', () => ({ useUpdateStatus }))
vi.mock('@/components/replay/api', () => ({
  useReplayAvailability,
}))
vi.mock('@/lib/shortcuts', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/shortcuts')>(),
  useShortcuts: (map: Record<string, () => void>) => { shortcuts.current = map },
}))
vi.mock('./paletteLoader', () => ({ loadPalette }))
vi.mock('@/lib/theme', () => ({ useTheme: () => ({ theme: 'light', toggle: toggleTheme }) }))
vi.mock('@/lib/session-lifecycle', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/session-lifecycle')>(),
  requestLogout,
}))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import type { Me, Membership } from '@/lib/me'
import { ui } from '@/messages/ui'

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

describe('AppShell live visitor badge', () => {
  beforeEach(() => {
    Element.prototype.scrollIntoView = () => undefined
    loadPalette.mockReset()
    loadPalette.mockImplementation(async () => (await vi.importActual<typeof import('./paletteLoader')>('./paletteLoader')).loadPalette())
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
    useUpdateStatus.mockReturnValue({ data: undefined, isError: false })
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
    toast.dismiss()
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

  it.each(['viewer', 'editor'] as const)('hides Add site from the switcher for a %s', async (role) => {
    renderShell('/app/acme/docs/settings/general', { ...membership, role })
    const sidebar = await screen.findByRole('complementary', { name: 'Application sidebar' })
    fireEvent.pointerDown(within(sidebar).getByRole('button', { name: /Docs\s*Acme/ }), { button: 0, ctrlKey: false, pointerType: 'mouse' })
    const menu = await screen.findByRole('menu')
    expect(within(menu).getByRole('menuitem', { name: /Other/ })).toBeInTheDocument()
    expect(within(menu).queryByRole('menuitem', { name: /Add site/ })).toBeNull()
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

  it('lists every registered keyboard shortcut in the help dialog', async () => {
    renderShell('/app/acme/docs/settings/general')
    fireEvent.click(await screen.findByRole('button', { name: 'Show keyboard shortcuts' }))
    const dialog = await screen.findByRole('dialog', { name: 'Keyboard shortcuts' })
    expect(within(dialog).getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      'D — Toggle dark mode',
      '? — Show this help',
      'Ctrl/⌘ B — Collapse or expand the sidebar',
      'Ctrl/⌘ K — Open the command palette',
      'M — Show the last 30 days (dashboard)',
      'C — Turn period comparison on or off (dashboard)',
    ])
  })

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
    expect(useUpdateStatus).toHaveBeenCalledWith(true)
  })

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

  it('returns focus to the header trigger after the theme command when opened by the shortcut', async () => {
    renderShell('/app/acme/docs/settings/general')
    const header = await screen.findByRole('banner', { name: 'Application header' })
    const trigger = within(header).getByRole('button', { name: 'Open command palette' })
    act(() => shortcuts.current['mod+k']())
    const dialog = await screen.findByRole('dialog', { name: 'Command palette' })
    fireEvent.click(within(dialog).getByRole('option', { name: 'Use dark theme' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument())
    await waitFor(() => expect(trigger).toHaveFocus())
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

  it('offers the language menu on account and admin pages with the saved language checked', async () => {
    const withLocale: Me = { ...me, user: { ...me.user, instance_admin: true }, preferences: { landing_page: 'overview', sites_view: 'cards', locale: 'es' } }
    for (const path of ['/app/account/profile', '/app/admin/users']) {
      const view = renderAppShell(path, withLocale)
      const header = await screen.findByRole('banner', { name: 'Application header' })
      fireEvent.keyDown(within(header).getByRole('button', { name: 'Language' }), { key: 'ArrowDown' })
      expect(await screen.findByRole('menuitemradio', { name: /^Español/ })).toHaveAttribute('aria-checked', 'true')
      view.unmount()
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
    const header = await screen.findByRole('banner', { name: 'Application header' })
    fireEvent.keyDown(document, { key: 'x' })
    expect(loadPalette).not.toHaveBeenCalled()
    expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument()
    expect(document.querySelector('[cmdk-root]')).toBeNull()
    fireEvent.click(within(header).getByRole('button', { name: 'Open command palette' }))
    expect(await screen.findByRole('dialog', { name: 'Command palette' })).toBeVisible()
    expect(loadPalette).toHaveBeenCalledOnce()
  })

  it('keeps the shell usable when the palette code fails to load, and retries on the next open', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      loadPalette.mockRejectedValueOnce(new Error('Failed to fetch dynamically imported module'))
      renderShell('/app/acme/docs/settings/general')
      const header = await screen.findByRole('banner', { name: 'Application header' })
      const trigger = within(header).getByRole('button', { name: 'Open command palette' })
      fireEvent.click(trigger)
      expect(await screen.findByText('The command palette could not be loaded. Check your connection and try again.')).toBeVisible()
      expect(screen.getByRole('banner', { name: 'Application header' })).toBeInTheDocument()
      expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument()

      fireEvent.click(trigger)
      expect(await screen.findByRole('dialog', { name: 'Command palette' })).toBeVisible()
      expect(loadPalette).toHaveBeenCalledTimes(2)
    } finally {
      consoleError.mockRestore()
    }
  })

  it('does not retry a failing palette load until the next open', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      loadPalette.mockRejectedValue(new Error('offline'))
      renderShell('/app/acme/docs/settings/general')
      const header = await screen.findByRole('banner', { name: 'Application header' })
      const trigger = within(header).getByRole('button', { name: 'Open command palette' })
      fireEvent.click(trigger)
      await screen.findByText(ui.palette.loadFailed)
      await new Promise((resolve) => setTimeout(resolve, 300))
      expect(loadPalette).toHaveBeenCalledTimes(1)
      expect(screen.getAllByText(ui.palette.loadFailed)).toHaveLength(1)
      expect(screen.getByRole('banner', { name: 'Application header' })).toBeInTheDocument()

      loadPalette.mockImplementation(async () => (await vi.importActual<typeof import('./paletteLoader')>('./paletteLoader')).loadPalette())
      fireEvent.click(trigger)
      expect(await screen.findByRole('dialog', { name: 'Command palette' })).toBeVisible()
      expect(loadPalette).toHaveBeenCalledTimes(2)
    } finally {
      consoleError.mockRestore()
    }
  })

  it('ignores Ctrl/⌘ K while the shortcut help dialog is open', async () => {
    renderShell('/app/acme/docs/settings/general')
    await screen.findByRole('banner', { name: 'Application header' })
    act(() => shortcuts.current['?']())
    expect(await screen.findByRole('dialog', { name: 'Keyboard shortcuts' })).toBeVisible()
    act(() => shortcuts.current['mod+k']())
    expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument()
    expect(loadPalette).not.toHaveBeenCalled()
  })

})
