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
      sites: [{ id: 7, name: 'Example', slug: 'example', allowed_hosts: ['supermetry.com'], tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }],
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
    get.mockImplementation((path: string, options?: { params?: { query?: Record<string, unknown> } }) => {
      if (path === '/api/private/stats/breakdown') {
        const query = options?.params?.query ?? {}
        const dimensions = (query.dimension ?? []) as string[]
        const metrics = String(query.metrics ?? 'visitors').split(',')
        const filters = (query.filter ?? []) as string[]
        let rows: Array<Record<string, unknown>> = []
        if (dimensions.join(',') === 'country') rows = [{ country: 'IN', visitors: 100, pageviews: 200 }]
        else if (dimensions.join(',') === 'weekday,hour_of_day') rows = [{ weekday: 1, hour_of_day: 13, visitors: 89, pageviews: 178 }]
        else if (filters.includes('country:is:IN')) rows = [{ [dimensions[0] ?? 'page']: 'Example', [metrics[0]]: 5 }]
        return Promise.resolve({
          data: { columns: [...dimensions, ...metrics], rows, totals: {}, meta: { sources_used: ['raw'], cached: false } },
          response: new Response(null, { status: 200 }),
        })
      }
      return Promise.resolve({ data: me, response: new Response(null, { status: 200 }) })
    })
    post.mockImplementation(
      (_path: string, options: { body: { dimensions?: string[]; metrics: string[] } }) => {
        const dimensions = options.body.dimensions ?? []
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
            columns: [...dimensions, metric],
            rows: [{ [dimension]: label, ...(dimension !== 'country' && dimensions.includes('country') ? { country: 'US' } : {}), ...totals }],
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

  function renderAt(...urls: string[]) {
    const history = createMemoryHistory({ initialEntries: urls, initialIndex: urls.length - 1 })
    const router = createRouter({ routeTree, history, parseSearch, stringifySearch })
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><RouterProvider router={router} /></QueryClientProvider>)
    return router
  }

  it('puts the date range, segments and include-imported in one sticky filter row and Export in the header actions', async () => {
    renderDashboard()
    const row = await screen.findByRole('group', { name: 'Filters' })
    expect(row).toHaveClass('sticky', 'top-11', 'z-10', 'bg-background')
    const date = within(row).getByRole('button', { name: /date range/i })
    const segments = within(row).getByRole('button', { name: 'Segments' })
    const imported = within(row).getByRole('checkbox', { name: 'Include imported data' })
    expect(within(row).getByRole('combobox', { name: 'Compare' })).toBeVisible()
    expect(date.compareDocumentPosition(segments) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(segments.compareDocumentPosition(imported) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    const actions = document.querySelector('[data-slot="page-header-actions"]') as HTMLElement
    expect(within(actions).getByRole('button', { name: 'Export CSV' })).toBeVisible()
    expect(within(actions).queryByRole('checkbox')).toBeNull()
    expect(within(row).queryByRole('button', { name: 'Export CSV' })).toBeNull()
  })

  it('publishes the filter row height as --dashboard-filter-h so the sticky inspector clears a wrapped row', async () => {
    class HeightObserver {
      constructor(private callback: ResizeObserverCallback) {}
      observe(target: Element) {
        const size = [{ inlineSize: 800, blockSize: 120 }]
        this.callback([{ target, contentRect: { width: 800, height: 104 }, borderBoxSize: size, contentBoxSize: size } as unknown as ResizeObserverEntry], this as unknown as ResizeObserver)
      }
      unobserve() {}
      disconnect() {}
    }
    vi.stubGlobal('ResizeObserver', HeightObserver)
    try {
      renderDashboard()
      const row = await screen.findByRole('group', { name: 'Filters' })
      await waitFor(() => expect((row.parentElement as HTMLElement).style.getPropertyValue('--dashboard-filter-h')).toBe('120px'))
    } finally {
      cleanup()
      vi.unstubAllGlobals()
    }
  })

  it('renders Where and Weekly rhythm with dashboard state and lazily filters from country details', async () => {
    const history = createMemoryHistory({ initialEntries: ['/app/acme/example?period=custom&from=2026-09-28&to=2026-09-30&metric=pageviews&compare=previous_year&include_imported=false'] })
    const router = createRouter({ routeTree, history, parseSearch, stringifySearch })
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><RouterProvider router={router} /></QueryClientProvider>)

    expect(await screen.findByRole('region', { name: 'Where' })).toBeVisible()
    expect(screen.getByRole('region', { name: 'Weekly rhythm' })).toBeVisible()
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1)
    expect(screen.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
    expect(within(screen.getByRole('heading', { level: 1, name: 'Dashboard' }).parentElement as HTMLElement).getByText(/^\S.* – .+\d{4}$/)).toBeVisible()
    expect(screen.queryByText(/Example, /)).toBeNull()
    const band = screen.getByTestId('where-when-band')
    expect(band).toHaveClass('lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]')
    expect(band).toHaveClass('items-start')
    expect(band).not.toHaveClass('items-stretch')
    expect(Array.from(band.children).map((child) => child.getAttribute('data-dashboard-band-item'))).toEqual(['where', 'weekly-rhythm', 'goals-events'])
    expect(band.querySelector('[data-dashboard-band-item="weekly-rhythm"]')).toHaveClass('lg:col-start-2', 'lg:row-span-2')
    expect(band.querySelector('[data-dashboard-band-item="goals-events"]')).toHaveClass('lg:col-start-1', 'lg:row-start-2')
    await waitFor(() => expect(get.mock.calls.filter(([path]) => path === '/api/private/stats/breakdown').length).toBeGreaterThanOrEqual(2))
    const overviewRequests = get.mock.calls
      .filter(([path]) => path === '/api/private/stats/breakdown')
      .map(([, options]) => options.params.query)
    expect(overviewRequests).toEqual(expect.arrayContaining([
      expect.objectContaining({ dimension: ['country'], metrics: 'visitors,pageviews', compare: 'previous_year', include_imported: false }),
      expect.objectContaining({ dimension: ['weekday', 'hour_of_day'], metrics: 'pageviews', compare: 'previous_year', include_imported: false }),
    ]))
    expect(await screen.findByRole('button', { name: 'India: 200 pageviews, 100 percent' }, { timeout: 5_000 })).toBeVisible()
    expect(screen.getByRole('gridcell', { name: 'Monday 1 PM: 178 pageviews' })).toBeVisible()

    fireEvent.click(screen.getByRole('button', { name: /India, rank 1/i }))
    expect(await screen.findByRole('heading', { name: /India/ })).toBeVisible()
    await waitFor(() => expect(get.mock.calls.filter(([path], index) => path === '/api/private/stats/breakdown' && ((get.mock.calls[index]?.[1]?.params?.query?.filter ?? []) as string[]).includes('country:is:IN')).length).toBeGreaterThanOrEqual(4))
    fireEvent.click(screen.getByRole('button', { name: 'Filter the dashboard to this country' }))
    await waitFor(() => expect(router.state.location.search.f).toEqual(['country:is:IN']))
  }, 15_000)

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
    fireEvent.click(await screen.findByRole('button', { description: 'Filter by country India' }))
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

    const locations = await screen.findByRole('region', { name: 'Location' })
    expect(
      within(locations)
        .getAllByRole('tab')
        .map((tab) => tab.textContent),
    ).toEqual(['Countries', 'Regions', 'Cities', 'Language'])
    expect(within(locations).queryByRole('button', { name: 'Map' })).not.toBeInTheDocument()
    expect(within(locations).queryByRole('link', { name: 'Open full map' })).not.toBeInTheDocument()

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

    const devices = await screen.findByRole('region', { name: 'Environment' })
    expect(within(devices).getAllByRole('tab').map((tab) => tab.textContent)).toEqual([
      'Browser',
      'Browser version',
      'OS',
      'OS version',
      'Device',
      'Screen size',
    ])

    const locations = screen.getByRole('region', { name: 'Location' })
    expect(within(locations).queryByRole('tab', { name: 'Hostname' })).not.toBeInTheDocument()
  })

  it.each([
    ['multiple hosts', ['supermetry.com', 'docs.supermetry.com']],
    ['a wildcard host', ['*.supermetry.com']],
  ])('shows Hostname for a site with %s', async (_case, allowedHosts) => {
    get.mockResolvedValue({
      data: {
        ...me,
        memberships: [{ ...me.memberships[0], sites: [{ ...me.memberships[0].sites[0], allowed_hosts: allowedHosts }] }],
      },
      response: new Response(null, { status: 200 }),
    })

    renderDashboard()

    const locations = await screen.findByRole('region', { name: 'Location' })
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
        description: 'Filter by channel Organic Search',
      }),
    )
    await waitFor(() =>
      expect(router.state.location.search.f).toEqual([
        'channel:is:Organic%20Search',
      ]),
    )
  })

  it.each([
    ['Regions', 'region', 'California', 'region:is:California,US'],
    ['Cities', 'city', 'San Francisco', 'city:is:San%20Francisco,US'],
  ])(
    'clicking a %s row adds its filter to router search',
    async (tab, dimension, label, encoded) => {
      const router = renderDashboard()
      const locations = await screen.findByRole('region', { name: 'Location' })

      fireEvent.click(within(locations).getByRole('tab', { name: tab }))
      fireEvent.click(
        await within(locations).findByRole('button', {
          description: `Filter by ${dimension} ${label}, United States`,
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

    fireEvent.click(await screen.findByRole('button', { description: 'Filter by country India' }))

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

  it('validates the inspect selection in the dashboard search and drops an unknown one', async () => {
    const leafSearch = (router: ReturnType<typeof renderAt>) => router.state.matches.at(-1)?.search as Record<string, unknown> | undefined
    const valid = renderAt('/app/acme/example?period=7d&inspect=country%3AIN')
    await waitFor(() => expect(leafSearch(valid)).toMatchObject({ period: '7d', inspect: 'country:IN' }))
    cleanup()
    const invalid = renderAt('/app/acme/example?period=7d&inspect=nope%3A1')
    await waitFor(() => expect(leafSearch(invalid)).toMatchObject({ period: '7d' }))
    expect(leafSearch(invalid)?.inspect).toBeUndefined()
  })

  const range = '/app/acme/example?period=custom&from=2026-09-28&to=2026-09-30'
  const anonymousMe = { ...me, memberships: [{ ...me.memberships[0], sites: [{ ...me.memberships[0].sites[0], tracking_policy: { instance_mode: 'anonymous', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }] }] }

  it('restores the inspector from the URL without touching filters and closes it with Esc', async () => {
    const router = renderAt(`${range}&inspect=country%3AIN`)
    const inspector = await screen.findByRole('dialog', { name: 'India' })
    expect(inspector).toHaveAccessibleDescription('Country')
    expect(await within(inspector).findByRole('region', { name: 'Metrics for this value' })).toBeVisible()
    expect(post).toHaveBeenCalledWith('/api/private/query', expect.objectContaining({
      body: expect.objectContaining({
        metrics: ['visitors', 'visits', 'pageviews', 'views_per_visit', 'bounce_rate', 'visit_duration', 'engagement_time'],
        filters: [{ dimension: 'country', operator: 'is', values: ['IN'] }],
      }),
    }))
    expect(router.state.location.search.f ?? []).toEqual([])

    fireEvent.keyDown(inspector, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'India' })).toBeNull())
    expect(router.state.location.search.inspect).toBeUndefined()
    expect(router.state.location.href).not.toContain('inspect')
    expect(router.state.location.search.period).toBe('custom')
  })

  it('pushes when the inspector opens so Back closes it, and replaces when it closes so Back never reopens it', async () => {
    const router = renderAt(range)
    await screen.findByRole('group', { name: 'Filters' })
    const start = router.history.length

    // Opening pushes one entry (what the Inspect controls do; they arrive with the sources).
    await router.navigate({ to: '/app/$org/$site', params: { org: 'acme', site: 'example' }, search: { ...router.state.location.search, inspect: 'country:IN' } as never })
    const inspector = await screen.findByRole('dialog', { name: 'India' })
    expect(router.history.length).toBe(start + 1)

    fireEvent.keyDown(inspector, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'India' })).toBeNull())
    expect(router.history.length).toBe(start + 1)
    expect(router.state.location.href).not.toContain('inspect')

    router.history.back()
    await waitFor(() => expect(router.state.location.href).not.toContain('inspect'))
    expect(screen.queryByRole('dialog', { name: 'India' })).toBeNull()
  })

  it('closes the inspector on Back from the opened state', async () => {
    const router = renderAt(range)
    await screen.findByRole('group', { name: 'Filters' })
    await router.navigate({ to: '/app/$org/$site', params: { org: 'acme', site: 'example' }, search: { ...router.state.location.search, inspect: 'country:IN' } as never })
    await screen.findByRole('dialog', { name: 'India' })
    router.history.back()
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'India' })).toBeNull())
    expect(router.state.location.href).not.toContain('inspect')
  })

  it('ignores the inspect param on a saved dashboard', async () => {
    renderAt(`${range}&dash=5&inspect=country%3AIN`)
    await waitFor(() => expect(get).toHaveBeenCalled())
    await new Promise((resolve) => setTimeout(resolve, 100))
    expect(screen.queryByRole('dialog', { name: 'India' })).toBeNull()
    expect(post.mock.calls.some(([, options]) => (options.body.filters ?? []).some((filter: { dimension: string }) => filter.dimension === 'country'))).toBe(false)
  })

  it('ignores the inspect param in edit mode', async () => {
    const router = renderAt(range)
    fireEvent.click(await screen.findByRole('button', { name: 'New dashboard' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Start editing' }))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'New dashboard' })).toBeNull())
    await router.navigate({ to: '/app/$org/$site', params: { org: 'acme', site: 'example' }, search: { ...router.state.location.search, inspect: 'country:IN' } as never })
    await new Promise((resolve) => setTimeout(resolve, 100))
    expect(screen.queryByRole('dialog', { name: 'India' })).toBeNull()
  })

  it('adds the inspected value as a filter and keeps the inspector open', async () => {
    const router = renderAt(`${range}&inspect=country%3AIN`)
    const inspector = await screen.findByRole('dialog', { name: 'India' })
    fireEvent.click(await within(inspector).findByRole('button', { name: 'Add as filter' }))
    await waitFor(() => expect(router.state.location.search.f).toEqual(['country:is:IN']))
    expect(router.state.location.search.inspect).toBe('country:IN')
    expect(await within(screen.getByRole('dialog', { name: 'India' })).findByText('This value is already a filter.')).toBeVisible()
  })

  it('opens only privacy-safe selections on an anonymous site', async () => {
    get.mockResolvedValue({ data: anonymousMe, response: new Response(null, { status: 200 }) })
    renderAt(`${range}&inspect=page%3A%252F`)
    await screen.findByRole('region', { name: 'Verified pages' })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(post.mock.calls.some(([, options]) => (options.body.filters ?? []).some((filter: { dimension: string }) => filter.dimension === 'page'))).toBe(false)
    cleanup()

    get.mockClear()
    renderAt(`${range}&inspect=country%3AIN`)
    expect(await screen.findByRole('dialog', { name: 'India' })).toBeVisible()
    const scoped = () => get.mock.calls.filter(([path, options]) => path === '/api/private/stats/breakdown' && ((options?.params?.query?.filter ?? []) as string[]).includes('country:is:IN'))
    await waitFor(() => expect(scoped().length).toBeGreaterThanOrEqual(2))
    expect(new Set(scoped().flatMap(([, options]) => options.params.query.dimension as string[]))).toEqual(new Set(['channel', 'device']))
  })

  it('opens the inspector from real Inspect controls without filtering, toggles, switches, and returns focus', async () => {
    const router = renderAt(range)
    const start = router.history.length
    const location = await screen.findByRole('region', { name: 'Location' })
    const inspect = await within(location).findByRole('button', { name: 'Inspect India' })
    expect(inspect).toHaveAttribute('aria-pressed', 'false')
    fireEvent.click(inspect)
    await waitFor(() => expect(router.state.location.search.inspect).toBe('country:IN'))
    expect(router.state.location.search.f ?? []).toEqual([])
    expect(router.history.length).toBe(start + 1)
    expect(await screen.findByRole('dialog', { name: 'India' })).toBeVisible()
    expect(within(location).getByRole('button', { name: 'Inspect India', hidden: true })).toHaveAttribute('aria-pressed', 'true')

    // Same row again closes by replace: no extra history entry.
    fireEvent.click(within(location).getByRole('button', { name: 'Inspect India', hidden: true }))
    await waitFor(() => expect(router.state.location.search.inspect).toBeUndefined())
    expect(router.history.length).toBe(start + 1)
    await waitFor(() => expect(inspect).toHaveFocus())

    // Another row replaces the content.
    fireEvent.click(inspect)
    await screen.findByRole('dialog', { name: 'India' })
    const other = (await screen.findAllByRole('button', { name: /^Inspect /, hidden: true })).find((button) => button.getAttribute('aria-label') !== 'Inspect India')
    expect(other).toBeDefined()
    fireEvent.click(other as HTMLElement)
    await waitFor(() => expect(router.state.location.search.inspect).not.toBe('country:IN'))
    expect(router.state.location.search.inspect).toBeTruthy()
    const otherName = (other as HTMLElement).getAttribute('aria-label')?.replace(/^Inspect /, '') as string
    expect(await screen.findByRole('dialog', { name: otherName })).toBeVisible()
    expect(screen.queryByRole('dialog', { name: 'India' })).toBeNull()

    // Esc returns focus to the row that opened the inspector last, not the first one.
    fireEvent.keyDown(screen.getByRole('dialog', { name: otherName }), { key: 'Escape' })
    await waitFor(() => expect(router.state.location.search.inspect).toBeUndefined())
    await waitFor(() => expect(screen.getAllByRole('button', { name: `Inspect ${otherName}` })[0]).toHaveFocus())
    expect(inspect).not.toHaveFocus()
    fireEvent.click(inspect)
    await screen.findByRole('dialog', { name: 'India' })
    fireEvent.click(screen.getAllByRole('button', { name: `Inspect ${otherName}`, hidden: true })[0])
    await screen.findByRole('dialog', { name: otherName })

    router.history.back()
    await waitFor(() => expect(router.state.location.search.inspect).toBe('country:IN'))
    router.history.back()
    await waitFor(() => expect(router.state.location.search.inspect).toBeUndefined())
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(router.state.location.search.f ?? []).toEqual([])
  }, 10_000)

  it('returns focus to the Inspect button when the inspector closes with Esc', async () => {
    const router = renderAt(range)
    const location = await screen.findByRole('region', { name: 'Location' })
    const inspect = await within(location).findByRole('button', { name: 'Inspect India' })
    fireEvent.click(inspect)
    const inspector = await screen.findByRole('dialog', { name: 'India' })
    fireEvent.keyDown(inspector, { key: 'Escape' })
    await waitFor(() => expect(router.state.location.search.inspect).toBeUndefined())
    await waitFor(() => expect(inspect).toHaveFocus())
  })

  it('keeps the row filter control working beside Inspect', async () => {
    const router = renderAt(range)
    const location = await screen.findByRole('region', { name: 'Location' })
    fireEvent.click(await within(location).findByRole('button', { description: 'Filter by country India' }))
    await waitFor(() => expect(router.state.location.search.f).toEqual(['country:is:IN']))
    expect(router.state.location.search.inspect).toBeUndefined()
  })

  it('offers Inspect on an anonymous site only for allowed dimensions', async () => {
    get.mockResolvedValue({ data: anonymousMe, response: new Response(null, { status: 200 }) })
    renderAt(range)
    await screen.findByRole('region', { name: 'Verified pages' })
    expect(await screen.findByRole('button', { name: 'Inspect India' })).toBeVisible()
    expect(screen.queryByRole('button', { name: /^Inspect \// })).toBeNull()

    // Blocked tabs (Regions, Browser version, ...) never reach an anonymous card, the verified pages card
    // offers no row control, and a value outside the canonical anonymous set (the fixture's browser
    // 'Direct') is filterable-looking but not inspectable.
    expect(within(screen.getByRole('region', { name: 'Verified pages' })).queryByRole('button', { name: /^Inspect / })).toBeNull()
    const environment = screen.getByRole('region', { name: 'Environment' })
    expect(within(environment).getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['Browser', 'OS', 'Device'])
    expect(await within(environment).findByRole('button', { description: 'Filter by browser Direct' })).toBeVisible()
    expect(within(environment).queryByRole('button', { name: /^Inspect / })).toBeNull()
    expect(within(screen.getByRole('region', { name: 'Channels' })).getByRole('button', { name: 'Inspect Organic Search' })).toBeVisible()
    expect(screen.queryByRole('region', { name: 'Location' })?.querySelector('[role="tab"]')).toBeNull()
    expect(screen.getByRole('button', { name: 'Inspect India' })).toBeVisible()
  })

  it('opens the inspector from a Where country and from a Weekly rhythm cell', async () => {
    const router = renderAt(range)
    const where = await screen.findByRole('region', { name: 'Where' })
    fireEvent.click(await within(where).findByRole('button', { name: 'Inspect India' }, { timeout: 5_000 }))
    await waitFor(() => expect(router.state.location.search.inspect).toBe('country:IN'))
    fireEvent.keyDown(await screen.findByRole('dialog', { name: 'India' }), { key: 'Escape' })
    await waitFor(() => expect(router.state.location.search.inspect).toBeUndefined())

    fireEvent.click(screen.getByRole('gridcell', { name: 'Monday 1 PM: 89 visitors' }))
    await waitFor(() => expect(router.state.location.search.inspect).toBe('weekday_hour:1,13'))
    const cell = await screen.findByRole('dialog', { name: 'Monday 1 PM' })
    expect(await within(cell).findByText('89')).toBeVisible()
    expect(within(cell).queryByRole('button', { name: 'Add as filter' })).toBeNull()
    expect(router.state.location.search.f ?? []).toEqual([])
  }, 15_000)
})
