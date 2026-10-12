import '@testing-library/jest-dom/vitest'

import { QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import {
  createMemoryHistory,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  canCreateSites,
  navigationHref,
  organizationHref,
  visibleNavigation,
} from '@/components/Sidebar'
import { queryClient } from '@/lib/queryClient'
import { routeTree } from '@/routeTree.gen'
import { loginRedirect } from './app.$org'

vi.mock('@/lib/session-lifecycle', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/session-lifecycle')>()
  return { ...actual, expireSession: vi.fn() }
})

const identifiedSite = { id: 7, name: 'Docs', tracking_policy: { instance_mode: 'identified' as const, effective_mode: 'anonymous' as const, configured_profiles_enabled: false, restrictions: [] } }

const me = {
  user: { id: 1, name: 'Owner', email: 'owner@example.test' },
  memberships: [
    {
      org: { id: 1, name: 'Acme', slug: 'acme' },
      role: 'owner',
      sites: [{ id: 7, name: 'Docs', public_id: 'docs-public' }],
    },
    {
      org: { id: 2, name: 'Other', slug: 'other' },
      role: 'viewer',
      sites: [],
    },
  ],
}

function meResponse(status = 200) {
  return vi.fn(async () =>
    new Response(status === 200 ? JSON.stringify(me) : '{}', {
      status,
      headers: { 'Content-Type': 'application/json' },
    }),
  )
}

function renderRoute(path: string) {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [path] }),
  })
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
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  vi.unstubAllGlobals()
})

describe('organization shell authentication', () => {
  it('redirects a signed-out organization request to login with the full destination', async () => {
    vi.stubGlobal('fetch', meResponse(401))
    const router = renderRoute('/app/missing?period=30d')

    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(router.state.location.search).toEqual({
      next: '/app/missing?period=30d',
    })
  })

  it('renders an in-route not-found page for an unavailable organization', async () => {
    vi.stubGlobal('fetch', meResponse())
    renderRoute('/app/missing')

    expect(
      await screen.findByText('We could not find that organisation'),
    ).toBeVisible()
    expect(screen.getByRole('link', { name: 'Acme' })).toHaveAttribute(
      'href',
      '/app/acme/docs-public',
    )
    expect(screen.getByRole('link', { name: 'Other' })).toHaveAttribute(
      'href',
      '/app/other/settings',
    )
    expect(screen.getByRole('link', { name: 'Dashboard' })).toHaveAttribute(
      'href',
      '/app',
    )
  })

  it('keeps a valid organization on its existing organization shell', async () => {
    vi.stubGlobal('fetch', meResponse())
    const router = renderRoute('/app/acme')

    expect(await screen.findByText('Acme', { exact: true })).toBeVisible()
    expect(router.state.location.pathname).toBe('/app/acme')
    expect(
      screen.queryByText('We could not find that organisation'),
    ).toBeNull()
  })

  it('preserves the full dashboard URL when authentication expires', () => {
    expect(loginRedirect('/app/acme/blog', '?period=30d')).toBe(
      '/login?next=%2Fapp%2Facme%2Fblog%3Fperiod%3D30d',
    )
  })

  it('hides management navigation from viewers', () => {
    const labels = visibleNavigation('viewer').map((item) => item.label)
    expect(labels).toContain('Dashboard')
    expect(labels).not.toContain('Settings')
    expect(labels).not.toContain('Data health')
  })

  it('switches organizations through the first allowed site', () => {
    expect(
      organizationHref({
        org: { id: 2, name: 'Other', slug: 'other' },
        role: 'viewer',
        sites: [{ id: 8, name: 'Docs', public_id: 'docs-public' }],
      }),
    ).toBe('/app/other/docs-public')
  })

  it('links data health at organization scope', () => {
    const health = visibleNavigation('owner').find(
      (item) => item.label === 'Data health',
    )
    expect(health && navigationHref(health, 'acme', 'blog')).toBe('/app/acme/health')
  })

  it('links Web Vitals at site scope', () => {
    const vitals = visibleNavigation('viewer', true, undefined, identifiedSite).find(
      (item) => item.label === 'Web Vitals',
    )
    expect(vitals && navigationHref(vitals, 'acme', 'blog')).toBe('/app/acme/blog/vitals')
  })

  it('links Heatmaps at site scope for every site role', () => {
    for (const role of ['owner', 'admin', 'editor', 'viewer'] as const) {
      const heatmaps = visibleNavigation(role, true, undefined, identifiedSite).find((item) => item.label === 'Heatmaps')
      expect(heatmaps && navigationHref(heatmaps, 'acme', 'blog')).toBe('/app/acme/blog/heatmaps')
    }
  })

  it('shows the organization Sites page to every role', () => {
    for (const role of ['owner', 'admin', 'editor', 'viewer'] as const) {
      expect(visibleNavigation(role).map((item) => item.label)).toContain('Sites')
    }
  })

  it('offers Add site only to roles that can create sites', () => {
    expect(canCreateSites('owner')).toBe(true)
    expect(canCreateSites('admin')).toBe(true)
    expect(canCreateSites('editor')).toBe(false)
    expect(canCreateSites('viewer')).toBe(false)
  })

  it('hides site-scoped navigation when the organization has no sites', () => {
    expect(visibleNavigation('owner', false).map((item) => item.label)).toEqual([
      'Sites',
      'Data health',
    ])
  })
})
