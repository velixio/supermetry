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
      effective_capabilities: ['profiles.read', 'replay.read', 'data.export', 'billing.manage'],
      sites: [{ id: 7, name: 'Example', slug: 'example', allowed_hosts: ['example.com'], tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }],
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
            rows: [{ [dimension]: label, ...totals }],
            totals,
            meta: { sources_used: ['daily'], cached: false, ...(dimension === 'page' ? { privacy: { view: 'verified_paths', coverage: 'verified_only', policy_id: 'fixture-policy', path_sanitizer_version: 1, imports_excluded: true, unverified_excluded: true } } : {}) },
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
        '/app/acme/example?period=custom&from=2026-09-28&to=2026-09-30',
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

  it('uses only safe anonymous breakdowns and labels the verified page subset', async () => {
    get.mockResolvedValue({ data: { ...me, memberships: [{ ...me.memberships[0], sites: [{ ...me.memberships[0].sites[0], tracking_policy: { instance_mode: 'anonymous', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: ['instance_anonymous'] } }] }] }, response: new Response(null, { status: 200 }) })
    renderDashboard()
    await screen.findByRole('region', { name: 'Verified pages' })
    await waitFor(() => expect(post.mock.calls.some(([, options]) => options.body.dimensions?.[0] === 'country')).toBe(true))
    const queries = post.mock.calls.filter(([path]) => path === '/api/private/query').map(([, options]) => options.body)
    for (const query of queries) {
      expect((query.dimensions ?? []).every((dimension: string) => ['time', 'hour', 'page', 'country', 'browser', 'os', 'device', 'channel'].includes(dimension))).toBe(true)
      if (query.dimensions?.[0] === 'page') {
        expect(query.metrics.every((metric: string) => ['visitors', 'visits', 'pageviews'].includes(metric))).toBe(true)
        expect(query.filters).toEqual([])
        expect(query.include_imported).toBe(false)
      }
    }
    expect(screen.getByText(/Partial coverage/)).toBeVisible()
    expect(screen.queryByRole('tab', { name: 'Entry' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Filter by page/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add annotation' })).not.toBeInTheDocument()
    expect(get.mock.calls.some(([path]) => /annotations|segments|suggestions/.test(path))).toBe(false)
  })

  it('blocks saved unsupported anonymous filters before fetching dashboard data', async () => {
    get.mockResolvedValue({ data: { ...me, memberships: [{ ...me.memberships[0], sites: [{ ...me.memberships[0].sites[0], tracking_policy: { instance_mode: 'anonymous', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }] }] }, response: new Response(null, { status: 200 }) })
    const history = createMemoryHistory({ initialEntries: ['/app/acme/example?f=source%3Ais%3Aprivate'] })
    const router = createRouter({ routeTree, history, parseSearch, stringifySearch })
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><RouterProvider router={router} /></QueryClientProvider>)
    expect(await screen.findByText(/These filters are unavailable in anonymous mode/)).toBeVisible()
    expect(post.mock.calls.filter(([path]) => path === '/api/private/query')).toHaveLength(0)
    fireEvent.click(screen.getByRole('button', { name: 'Clear unavailable filters' }))
    await screen.findByRole('region', { name: 'Verified pages' })
  })

  it('does not request analytics while the instance policy is unavailable', async () => {
    get.mockResolvedValue({ data: { ...me, memberships: [{ ...me.memberships[0], sites: [{ ...me.memberships[0].sites[0], tracking_policy: undefined }] }] }, response: new Response(null, { status: 200 }) })
    renderDashboard()
    expect(await screen.findByText('Tracking policy is unavailable')).toBeVisible()
    expect(post.mock.calls.filter(([path]) => path === '/api/private/query')).toHaveLength(0)
  })

  it('keeps canonical filters usable and pauses the separate verified page subset', async () => {
    get.mockResolvedValue({ data: { ...me, memberships: [{ ...me.memberships[0], sites: [{ ...me.memberships[0].sites[0], tracking_policy: { instance_mode: 'anonymous', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }] }] }, response: new Response(null, { status: 200 }) })
    renderDashboard()
    await screen.findByRole('region', { name: 'Verified pages' })
    fireEvent.click(await screen.findByRole('button', { name: /filter by country India/i }))
    expect(await screen.findByText(/Verified page analytics require no filters/)).toBeVisible()
    await waitFor(() => expect(post.mock.calls.some(([, options]) => options.body.dimensions?.[0] === 'country' && options.body.filters.length > 0)).toBe(true))
    expect(post.mock.calls.some(([, options]) => options.body.dimensions?.[0] === 'page' && options.body.filters.length > 0)).toBe(false)
    fireEvent.pointerDown(screen.getByRole('button', { name: 'Export CSV' }), { button: 0, ctrlKey: false })
    expect(await screen.findByRole('menuitem', { name: 'Verified pages (partial)' })).toHaveAttribute('aria-disabled', 'true')
    expect(screen.queryByRole('menuitem', { name: 'Sources' })).not.toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: 'Events' })).not.toBeInTheDocument()
  })

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

    fireEvent.click(await screen.findByRole('button', { name: /filter by country India/i }))

    await waitFor(() =>
      expect(router.state.location.search.f).toEqual(['country:is:IN']),
    )
  })

  it('shows the full country name in an active country filter chip', async () => {
    const history = createMemoryHistory({
      initialEntries: ['/app/acme/example?period=30d&f=country%3Ais%3AIN'],
    })
    const router = createRouter({ routeTree, history, parseSearch, stringifySearch })
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>)

    const chip = await screen.findByRole('button', { name: /country.*India/i })
    expect(chip).toHaveTextContent('country is India')
    expect(chip).not.toHaveTextContent('IN')
    expect(chip.querySelector('img')).toHaveAttribute('src', '/flags/in.svg')
  })

  // Exports one CSV per dashboard tab, so it needs more than the 5 s default
  // when the full suite runs in parallel.
  it('exports every dashboard CSV with the current range and filters', { timeout: 20_000 }, async () => {
    const history = createMemoryHistory({
      initialEntries: [
        '/app/acme/example?period=custom&from=2026-09-28&to=2026-09-30&f=country%3Ais%3AIN',
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
