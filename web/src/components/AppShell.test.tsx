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

const { useLiveSummary } = vi.hoisted(() => ({ useLiveSummary: vi.fn() }))

vi.mock('@/features/realtime/useLiveSummary', () => ({ useLiveSummary }))
vi.mock('@/components/replay/api', () => ({
  useReplayAvailability: () => ({ enabled: false }),
}))
vi.mock('@/lib/shortcuts', () => ({ useShortcuts: () => undefined }))
vi.mock('@/lib/theme', () => ({ useTheme: () => ({ theme: 'light', toggle: vi.fn() }) }))

import type { Me, Membership } from '@/routes/$org'

import { AppShell } from './AppShell'

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  sites: [{ id: 8, name: 'Other', slug: 'other' }, { id: 7, name: 'Docs', slug: 'docs', public_id: 'docs-public' }],
}

const me: Me = {
  user: { id: 1, name: 'Owner', email: 'owner@panel.test' },
  memberships: [membership],
}

function renderShell(initialEntry: string) {
  const root = createRootRoute({ component: Outlet })
  const org = createRoute({
    getParentRoute: () => root,
    path: '$org',
    component: () => <AppShell me={me} membership={membership} />,
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
      org.addChildren([
        site.addChildren([siteSettings.addChildren([siteGeneral]), realtime]),
        orgSettings.addChildren([orgMembers]),
      ]),
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
    const { router } = renderShell('/acme/docs-public/settings/general')

    const badge = await screen.findByRole('link', {
      name: '12 active now, 48 in the last 30 minutes',
    })
    expect(badge).toHaveTextContent('● 12 live')
    expect(badge).toHaveAttribute('href', '/acme/docs-public/realtime')

    fireEvent.focus(badge)
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      '12 active now, 48 in the last 30 minutes',
    )

    fireEvent.click(badge)
    await waitFor(() => expect(router.state.location.pathname).toBe('/acme/docs-public/realtime'))
  })

  it.each(['docs', 'docs-public', '7'])('keeps current site context for the %s alias', async (alias) => {
    renderShell(`/acme/${alias}/settings/general`)
    expect(await screen.findByRole('link', { name: '12 active now, 48 in the last 30 minutes' })).toHaveAttribute('href', `/acme/${alias}/realtime`)
    expect(useLiveSummary).toHaveBeenCalledWith(7)
    fireEvent.keyDown(screen.getByRole('button', { name: 'Settings' }), { key: 'ArrowDown' })
    expect(await screen.findByRole('menuitem', { name: /Site settings\s*Docs/ })).toHaveAttribute('href', '/acme/docs/settings/general')
  })

  it('uses the same live label for one visitor', async () => {
    useLiveSummary.mockReturnValue({
      data: { active_visitors: 1, visitors_last_30m: 9 },
      isPending: false,
      isError: false,
    })
    renderShell('/acme/docs-public/settings/general')

    expect(await screen.findByRole('link', {
      name: '1 active now, 9 in the last 30 minutes',
    })).toHaveTextContent('● 1 live')
  })

  it('shows a muted placeholder while loading and hides errors', async () => {
    useLiveSummary.mockReturnValue({ data: undefined, isPending: true, isError: false })
    const { unmount } = renderShell('/acme/docs-public/settings/general')
    expect((await screen.findByText('— live')).closest('output')).toHaveClass('text-muted-foreground')

    unmount()
    useLiveSummary.mockReturnValue({ data: undefined, isPending: false, isError: true })
    renderShell('/acme/docs-public/settings/general')
    expect(screen.queryByText(/live$/)).not.toBeInTheDocument()
  })

  it('does not show the badge on organization-only routes', async () => {
    renderShell('/acme/settings/members')

    expect(await screen.findByRole('heading', { name: 'Organization members' })).toBeVisible()
    expect(screen.queryByText(/live$/)).not.toBeInTheDocument()
    expect(useLiveSummary).not.toHaveBeenCalled()
  })
})
