import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
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
      sites: [{ id: 7, name: 'Example', slug: 'example', allowed_hosts: ['example.com'] }],
    },
  ],
}

describe('dashboard breakdown route', () => {
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
    get.mockResolvedValue({ data: me, response: new Response(null, { status: 200 }) })
    post.mockImplementation(
      (_path: string, options: { body: { dimensions?: string[]; metrics: string[] } }) => {
        const dimension = options.body.dimensions?.[0] ?? 'page'
        const metric = options.body.metrics[0]
        const label =
          dimension === 'country'
            ? 'IN'
            : dimension === 'region'
              ? 'California'
              : dimension === 'city'
                ? 'San Francisco'
                : dimension === 'channel'
                  ? 'Organic Search'
                : dimension === 'page'
                  ? '/'
                  : dimension === 'time'
                    ? '2026-09-28'
                    : 'Direct'
        const totals = Object.fromEntries(
          options.body.metrics.map((requestedMetric) => [requestedMetric, 5]),
        )
        return Promise.resolve({
          data: {
            columns: [dimension, metric],
            rows: [{ [dimension]: label, [metric]: 5 }],
            totals,
            meta: { sources_used: ['daily'], cached: false },
          },
          response: new Response(null, { status: 200 }),
        })
      },
    )
  })

  afterEach(cleanup)

  function renderDashboard() {
    const history = createMemoryHistory({
      initialEntries: [
        '/acme/example?period=custom&from=2026-09-28&to=2026-09-30',
      ],
    })
    const router = createRouter({ routeTree, history, parseSearch, stringifySearch })
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )
    return router
  }

  it('shows country, region, and city tabs without a referrer tab', async () => {
    renderDashboard()

    const locations = await screen.findByRole('region', { name: 'Countries' })
    expect(
      within(locations)
        .getAllByRole('tab')
        .map((tab) => tab.textContent),
    ).toEqual(['Countries', 'Regions', 'Cities', 'Language'])

    const sources = screen.getByRole('region', { name: 'Sources' })
    expect(within(sources).queryByRole('tab', { name: 'Referrer' })).not.toBeInTheDocument()
    expect(
      within(sources)
        .getAllByRole('tab')
        .map((tab) => tab.textContent),
    ).toEqual(['Source', 'Channels', 'UTM Source', 'Medium', 'Campaign'])
  })

  it('shows the new device tabs and hides Hostname for a single-host site', async () => {
    renderDashboard()

    const devices = await screen.findByRole('region', { name: 'Devices' })
    expect(within(devices).getAllByRole('tab').map((tab) => tab.textContent)).toEqual([
      'Browser',
      'Browser version',
      'OS',
      'OS version',
      'Device',
      'Screen size',
    ])

    const locations = screen.getByRole('region', { name: 'Countries' })
    expect(within(locations).queryByRole('tab', { name: 'Hostname' })).not.toBeInTheDocument()
  })

  it.each([
    ['multiple hosts', ['example.com', 'docs.example.com']],
    ['a wildcard host', ['*.example.com']],
  ])('shows Hostname for a site with %s', async (_case, allowedHosts) => {
    get.mockResolvedValue({
      data: {
        ...me,
        memberships: [{ ...me.memberships[0], sites: [{ ...me.memberships[0].sites[0], allowed_hosts: allowedHosts }] }],
      },
      response: new Response(null, { status: 200 }),
    })

    renderDashboard()

    const locations = await screen.findByRole('region', { name: 'Countries' })
    expect(within(locations).getByRole('tab', { name: 'Hostname' })).toBeVisible()
  })

  it('shows a Channels tab in Sources and filters by channel', async () => {
    const router = renderDashboard()
    const sources = await screen.findByRole('region', { name: 'Sources' })
    expect(within(sources).getAllByRole('tab').map((tab) => tab.textContent)).toEqual([
      'Source',
      'Channels',
      'UTM Source',
      'Medium',
      'Campaign',
    ])
    fireEvent.click(within(sources).getByRole('tab', { name: 'Channels' }))
    fireEvent.click(
      await within(sources).findByRole('button', {
        name: 'Filter by channel Organic Search',
      }),
    )
    await waitFor(() =>
      expect(router.state.location.search.f).toEqual([
        'channel:is:Organic%20Search',
      ]),
    )
  })

  it.each([
    ['Regions', 'region', 'California', 'region:is:California'],
    ['Cities', 'city', 'San Francisco', 'city:is:San%20Francisco'],
  ])(
    'clicking a %s row adds its filter to router search',
    async (tab, dimension, label, encoded) => {
      const router = renderDashboard()
      const locations = await screen.findByRole('region', { name: 'Countries' })

      fireEvent.click(within(locations).getByRole('tab', { name: tab }))
      fireEvent.click(
        await within(locations).findByRole('button', {
          name: `Filter by ${dimension} ${label}`,
        }),
      )

      await waitFor(() => expect(router.state.location.search.f).toEqual([encoded]))
    },
  )

  it('clicking a country row adds the is filter to router search', async () => {
    const router = renderDashboard()

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /visitors/i })).toHaveTextContent('5'),
    )
    expect(screen.getByRole('combobox', { name: 'Compare' })).toBeVisible()
    expect(screen.getByRole('table', { name: /visitors over time/i })).toBeVisible()

    fireEvent.keyDown(document, { key: 'm' })
    await waitFor(() => expect(router.state.location.search.period).toBe('30d'))

    fireEvent.click(await screen.findByRole('button', { name: /filter by country IN/i }))

    await waitFor(() =>
      expect(router.state.location.search.f).toEqual(['country:is:IN']),
    )
  })

  it('exports every dashboard CSV with the current range and filters', async () => {
    const history = createMemoryHistory({
      initialEntries: [
        '/acme/example?period=custom&from=2026-09-28&to=2026-09-30&f=country%3Ais%3AIN',
      ],
    })
    const router = createRouter({ routeTree, history, parseSearch, stringifySearch })
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )

    await screen.findByRole('region', { name: 'Pages' })
    post.mockClear()

    const exports = [
      ['Timeseries', 'time', 'visitors'],
      ['Pages', 'page', 'visitors'],
      ['Entry', 'entry', 'visitors'],
      ['Exit', 'exit', 'visitors'],
      ['Sources', 'source', 'visitors'],
      ['UTM', 'utm_source', 'visitors'],
      ['Countries', 'country', 'visitors'],
      ['Regions', 'region', 'visitors'],
      ['Cities', 'city', 'visitors'],
      ['Browsers', 'browser', 'visitors'],
      ['OS', 'os', 'visitors'],
      ['Devices', 'device', 'visitors'],
      ['Events', 'event', 'events'],
    ] as const

    for (const [label, dimension, metric] of exports) {
      fireEvent.pointerDown(screen.getByRole('button', { name: 'Export CSV' }), {
        button: 0,
        ctrlKey: false,
      })
      fireEvent.click(await screen.findByRole('menuitem', { name: label }))

      await waitFor(() =>
        expect(post).toHaveBeenCalledWith('/api/private/query', {
          body: {
            site_id: 7,
            date_range: { from: '2026-09-28', to: '2026-09-30' },
            timezone: 'UTC',
            metrics: [metric],
            dimensions: [dimension],
            filters: [
              { dimension: 'country', operator: 'is', values: ['IN'] },
            ],
            include_imported: true,
          },
          headers: { Accept: 'text/csv' },
          params: { query: { format: 'csv', limit: 'all' } },
          parseAs: 'text',
        }),
      )
    }

    expect(post).toHaveBeenCalledTimes(exports.length)
  })
})
