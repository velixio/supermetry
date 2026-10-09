import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  api: { GET: get, POST: post },
}))

import { routeTree } from '@/routeTree.gen'
import { parseSearch, stringifySearch } from '@/router'

const me = {
  user: { id: 1, name: 'Owner', email: 'owner@example.com' },
  memberships: [{
    org: { id: 1, name: 'Acme', slug: 'acme' },
    role: 'owner',
    sites: [{ id: 7, name: 'Example', slug: 'example', timezone: 'UTC', tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }],
  }],
}

function renderPath(path: string) {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [path] }),
    parseSearch,
    stringifySearch,
  })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>)
}

describe('report routes', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn().mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }),
    })
    Object.defineProperty(window, 'scrollTo', { configurable: true, value: vi.fn() })
    get.mockResolvedValue({ data: me, response: new Response(null, { status: 200 }) })
    post.mockImplementation((path: string) => Promise.resolve({
      data: path.includes('/journeys/')
        ? { nodes: [], links: [] }
        : path.includes('/retention/')
          ? { identity_window: 'day', retention_mode: 'within_day_session_return', cohorts: [] }
          : { columns: ['page', 'visitors'], rows: [], totals: {}, meta: { sources_used: [], cached: false } },
      response: new Response(null, { status: 200 }),
    }))
  })

  afterEach(cleanup)

  it('passes the dashboard date range and filters to journeys', async () => {
    renderPath('/acme/example/journeys?period=custom&from=2026-10-01&to=2026-10-08&f=country%3Ais%3AIN&steps=4')
    expect(await screen.findByRole('heading', { name: 'User journeys' })).toBeVisible()
    await waitFor(() => expect(post).toHaveBeenCalledWith(
      '/api/private/journeys/query',
      expect.objectContaining({ body: expect.objectContaining({
        date_range: { from: '2026-10-01', to: '2026-10-08' },
        filters: [{ dimension: 'country', operator: 'is', values: ['IN'] }],
        steps: 4,
      }) }),
    ))
  })

  it('passes the dashboard state and interval to retention', async () => {
    renderPath('/acme/example/retention?period=custom&from=2026-10-01&to=2026-10-08&f=device%3Ais%3Adesktop&interval=week')
    expect(await screen.findByRole('heading', { name: 'Retention' })).toBeVisible()
    await waitFor(() => expect(post).toHaveBeenCalledWith(
      '/api/private/retention/query',
      expect.objectContaining({ body: expect.objectContaining({
        date_range: { from: '2026-10-01', to: '2026-10-08' },
        filters: [{ dimension: 'device', operator: 'is', values: ['desktop'] }],
        interval: 'week',
      }) }),
    ))
  })
})
