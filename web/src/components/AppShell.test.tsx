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
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { useLiveSummary, useReplayAvailability } = vi.hoisted(() => ({ useLiveSummary: vi.fn(), useReplayAvailability: vi.fn() }))

vi.mock('@/features/realtime/useLiveSummary', () => ({ useLiveSummary }))
vi.mock('@/components/replay/api', () => ({
  useReplayAvailability,
}))
vi.mock('@/lib/shortcuts', () => ({ useShortcuts: () => undefined }))
vi.mock('@/lib/theme', () => ({ useTheme: () => ({ theme: 'light', toggle: vi.fn() }) }))

import type { Me, Membership } from '@/routes/app.$org'

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
  })

  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('shows counts, accessible detail, tooltip, and a realtime link on nested site routes', async () => {
    const { router } = renderShell('/app/acme/docs-public/settings/general')

    const badge = await screen.findByRole('link', {
      name: '12 active now, 48 in the last 30 minutes',
    })
    expect(badge).toHaveTextContent('● 12 live')
    expect(badge).toHaveAttribute('href', '/app/acme/docs-public/realtime')

    fireEvent.focus(badge)
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      '12 active now, 48 in the last 30 minutes',
    )

    fireEvent.click(badge)
    await waitFor(() => expect(router.state.location.pathname).toBe('/app/acme/docs-public/realtime'))
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
    expect(await screen.findByRole('link', { name: '12 active now, 48 in the last 30 minutes' })).toHaveAttribute('href', `/app/acme/${alias}/realtime`)
    expect(useLiveSummary).toHaveBeenCalledWith(7)
    fireEvent.keyDown(screen.getByRole('button', { name: 'Settings' }), { key: 'ArrowDown' })
    expect(await screen.findByRole('menuitem', { name: /Site settings\s*Docs/ })).toHaveAttribute('href', '/app/acme/docs/settings/general')
  })

  it('uses the same live label for one visitor', async () => {
    useLiveSummary.mockReturnValue({
      data: { active_visitors: 1, visitors_last_30m: 9 },
      isPending: false,
      isError: false,
    })
    renderShell('/app/acme/docs-public/settings/general')

    expect(await screen.findByRole('link', {
      name: '1 active now, 9 in the last 30 minutes',
    })).toHaveTextContent('● 1 live')
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
    expect(screen.getByLabelText('12 active now, 48 in the last 30 minutes')).toHaveTextContent('● 12 live')
    expect(screen.queryByRole('link', { name: '12 active now, 48 in the last 30 minutes' })).not.toBeInTheDocument()
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
})
