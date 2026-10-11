import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
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
  memberships: [
    {
      org: { id: 1, name: 'Acme', slug: 'acme' },
      role: 'owner',
      effective_capabilities: ['profiles.read', 'replay.read'],
      sites: [
        {
          id: 7,
          name: 'Example',
          slug: 'example',
          timezone: 'America/New_York',
          retention_days: 30,
          tracking_policy: {
            instance_mode: 'identified',
            effective_mode: 'identified',
            configured_profiles_enabled: true,
            restrictions: [],
          },
        },
      ],
    },
  ],
}

function queryResult(dimensions: string[], metrics: string[]) {
  if (dimensions.join(',') === 'country') {
    return {
      rows: [{ country: 'IN', visitors: 100, pageviews: 200 }],
      totals: { visitors: 100, pageviews: 200 },
    }
  }
  if (dimensions.join(',') === 'weekday,hour_of_day') {
    return {
      rows: [{ weekday: 5, hour_of_day: 19, visitors: 164, pageviews: 328 }],
      totals: { visitors: 164, pageviews: 328 },
    }
  }
  const labels: Record<string, string> = {
    page: '/pricing',
    region: 'Maharashtra',
    city: 'Mumbai',
    source: 'Google',
  }
  const dimension = dimensions[0]
  return {
    rows: [{ [dimension]: labels[dimension] ?? '', [metrics[0]]: 5 }],
    totals: { [metrics[0]]: 5 },
  }
}

describe('map route', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn().mockReturnValue({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    })
    Object.defineProperty(window, 'scrollTo', {
      configurable: true,
      value: vi.fn(),
    })
    get.mockImplementation((path: string, options?: { params?: { query?: Record<string, unknown> } }) => {
      if (path === '/api/private/me')
        return Promise.resolve({
          data: me,
          response: new Response(null, { status: 200 }),
        })
      if (path === '/api/private/realtime')
        return Promise.resolve({
          data: { active_visitors: 0, visitors_last_30m: 0, top_pages: [] },
        })
      if (path === '/api/private/stats/breakdown') {
        const query = options?.params?.query ?? {}
        const dimensions = query.dimension as string[]
        const metrics = String(query.metrics).split(',')
        const result = queryResult(dimensions, metrics)
        return Promise.resolve({
          data: {
            columns: [...dimensions, ...metrics],
            ...result,
            meta: { sources_used: ['raw'], cached: false },
          },
          response: new Response(null, { status: 200 }),
        })
      }
      return Promise.resolve({ data: {} })
    })
    post.mockResolvedValue({
      data: {
        columns: ['visitors', 'pageviews', 'bounce_rate', 'visit_duration'],
        rows: [],
        totals: {
          visitors: 100,
          pageviews: 200,
          bounce_rate: 25,
          visit_duration: 90,
        },
        meta: { sources_used: ['daily'], cached: false },
      },
      response: new Response(null, { status: 200 }),
    })
  })

  afterEach(cleanup)

  function renderMap(path = '/app/acme/example/map?period=30d&metric=visitors') {
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: [path] }),
      parseSearch,
      stringifySearch,
    })
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )
    return router
  }

  it('renders at the shareable route and drives map and traffic from the URL metric', async () => {
    const router = renderMap()
    expect(await screen.findByRole('heading', { name: 'Map' })).toBeVisible()
    expect(
      await screen.findByRole('button', {
        name: /India: 100 visitors, 100 percent/,
      }),
    ).toBeVisible()
    expect(screen.getByRole('gridcell', { name: 'Friday 7 PM: 164 visitors' })).toBeVisible()

    fireEvent.click(screen.getByRole('radio', { name: 'Pageviews' }))

    await waitFor(() => expect(router.state.location.search.metric).toBe('pageviews'))
    expect(
      await screen.findByRole('button', {
        name: /India: 200 pageviews, 100 percent/,
      }),
    ).toBeVisible()
    expect(screen.getByRole('gridcell', { name: 'Friday 7 PM: 328 pageviews' })).toBeVisible()
  })

  it('selects a country, lazily requests all detail breakdowns, and filters the dashboard', async () => {
    const router = renderMap()
    fireEvent.click(await screen.findByRole('button', { name: /India: 100 visitors/ }))

    expect(await screen.findByRole('heading', { name: /India/ })).toBeVisible()
    await waitFor(() => {
      const detailDimensions = get.mock.calls
        .filter(([path]) => path === '/api/private/stats/breakdown')
        .map(([, options]) => options.params.query)
        .filter((query) => ['page', 'region', 'city', 'source'].includes(query.dimension?.[0]))
      expect(detailDimensions).toHaveLength(4)
      for (const query of detailDimensions) expect(query.filter).toContain('country:is:IN')
    })

    fireEvent.click(
      screen.getByRole('button', {
        name: 'Filter the dashboard to this country',
      }),
    )
    await waitFor(() => expect(router.state.location.pathname).toBe('/app/acme/example'))
    expect(router.state.location.search.f).toEqual(['country:is:IN'])
  })
})
