import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post, useStats } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  useStats: vi.fn(),
}))

vi.mock('@/lib/api', () => ({ api: { GET: get, POST: post } }))
vi.mock('@/lib/stats', () => ({ useStats }))

import { GoalsPage } from './GoalsPage'

describe('GoalsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get
      .mockResolvedValueOnce({ data: [], response: { status: 200 } })
      .mockResolvedValue({
        data: [
          {
            id: 11,
            site_id: 7,
            kind: 'page',
            match: 'exact',
            value: '/thanks',
            name: 'Signup complete',
          },
        ],
        response: { status: 200 },
      })
    post.mockResolvedValue({
      data: {
        id: 11,
        site_id: 7,
        kind: 'page',
        match: 'exact',
        value: '/thanks',
        name: 'Signup complete',
      },
      response: { status: 201 },
    })
    useStats.mockReturnValue({
      data: { rows: [] },
      isPending: false,
      isError: false,
    })
  })

  afterEach(() => cleanup())

  it('shows a created goal after invalidating and refetching the goals list', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <GoalsPage
          siteId={7}
          dateRange={{ from: '2026-10-01', to: '2026-10-07' }}
        />
      </QueryClientProvider>,
    )

    await waitFor(() => expect(get).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('main')).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Goal name'), {
      target: { value: 'Signup complete' },
    })
    fireEvent.change(screen.getByLabelText('Goal value'), {
      target: { value: '/thanks' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Create goal' }))

    expect(await screen.findByText('Signup complete')).toBeVisible()
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('loads all goal conversions in one site-timezone breakdown query', async () => {
    get.mockReset()
    useStats.mockReset()
    get.mockResolvedValue({
      data: [
        { id: 4, site_id: 7, kind: 'page', match: 'exact', value: '/thanks', name: 'Signup complete' },
        { id: 5, site_id: 7, kind: 'page', match: 'exact', value: '/paid', name: 'Paid complete' },
      ],
      response: { status: 200 },
    })
    useStats.mockReturnValue({
      data: {
        rows: [
          { goal: '4', conversions: 8 },
          { goal: '5', conversions: 2 },
        ],
        totals: { visits: 40 },
      },
      isPending: false,
      isError: false,
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    render(<QueryClientProvider client={client}><GoalsPage siteId={7} timezone="Asia/Kolkata" dateRange={{ from: '2026-10-01', to: '2026-10-07' }} /></QueryClientProvider>)

    expect(await screen.findByText('Signup complete')).toBeVisible()
    expect(screen.getByText('8')).toBeVisible()
    expect(screen.getByText('20%')).toBeVisible()
    expect(screen.getByText('Paid complete')).toBeVisible()
    expect(screen.getByText('2')).toBeVisible()
    expect(screen.getByText('5%')).toBeVisible()
    expect(new Set(useStats.mock.calls.map(([statsQuery]) => JSON.stringify(statsQuery))).size).toBe(1)
    expect(useStats).toHaveBeenCalledWith({
      siteId: 7,
      dateRange: { from: '2026-10-01', to: '2026-10-07' },
      metrics: ['conversions', 'visits'],
      dimensions: ['goal'],
      filters: [],
      timezone: 'Asia/Kolkata',
    })
  })

  it('describes scroll-depth goals in the goals list', async () => {
    get.mockReset()
    get.mockResolvedValue({
      data: [{ id: 9, site_id: 7, kind: 'scroll', match: 'wildcard', value: '/blog/*', name: 'Engaged reader', scroll_threshold: 75 }],
      response: { status: 200 },
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    render(<QueryClientProvider client={client}><GoalsPage siteId={7} dateRange={{ from: '2026-10-01', to: '2026-10-07' }} /></QueryClientProvider>)

    expect(await screen.findByText('Scroll ≥ 75% on /blog/*')).toBeVisible()
  })

  it('shows currency-formatted revenue only for revenue goals', async () => {
    get.mockReset()
    useStats.mockReset()
    get.mockResolvedValue({
      data: [
        { id: 4, site_id: 7, kind: 'event', match: 'exact', value: 'purchase', name: 'Purchase', currency: 'USD' },
        { id: 5, site_id: 7, kind: 'page', match: 'exact', value: '/thanks', name: 'Signup complete' },
      ],
      response: { status: 200 },
    })
    useStats.mockImplementation((query: { metrics: string[]; filters: { dimension: string; values: string[] }[] }) => ({
      data: {
        totals: query.metrics.includes('total_revenue')
          ? { visitors: 2, total_revenue: 123.45, average_revenue: 61.725 }
          : query.filters.length > 0 ? { visitors: 8 } : { visitors: 80 },
      },
      isPending: false,
      isError: false,
    }))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    render(<QueryClientProvider client={client}><GoalsPage siteId={7} dateRange={{ from: '2026-10-01', to: '2026-10-07' }} /></QueryClientProvider>)

    expect(await screen.findByText('Purchase')).toBeVisible()
    expect(screen.getByText('$123.45')).toBeVisible()
    expect(screen.getByText('$61.73')).toBeVisible()
    expect(screen.getAllByText('—')).toHaveLength(2)
    expect(useStats).toHaveBeenCalledWith(expect.objectContaining({
      metrics: ['total_revenue', 'average_revenue'],
      filters: [
        { dimension: 'goal_id', operator: 'is', values: ['4'] },
        { dimension: 'revenue_currency', operator: 'is', values: ['USD'] },
      ],
    }))
  })
  it.each([
    { name: 'loading', state: { isPending: true }, text: 'Loading revenue…' },
    { name: 'refetching', state: { isFetching: true, data: { totals: { total_revenue: 0, average_revenue: 0 } } }, text: 'Loading revenue…' },
    { name: 'failed with stale zero', state: { isError: true, data: { totals: { total_revenue: 0, average_revenue: 0 } } }, text: 'Could not load revenue' },
    { name: 'missing response', state: {}, text: 'Revenue unavailable' },
    { name: 'missing totals', state: { data: { rows: [] } }, text: 'Revenue unavailable' },
    { name: 'missing metric', state: { data: { totals: {} } }, text: 'Revenue unavailable' },
    { name: 'nonfinite metric', state: { data: { totals: { total_revenue: NaN, average_revenue: Infinity } } }, text: 'Revenue unavailable' },
  ])('does not render zero for $name revenue', async ({ state, text }) => {
    const refetch = vi.fn()
    get.mockReset().mockResolvedValue({ data: [{ id: 4, site_id: 7, kind: 'event', match: 'exact', value: 'purchase', name: 'Purchase', currency: 'USD' }], response: { status: 200 } })
    useStats.mockImplementation((query: { metrics: string[] }) => query.metrics.includes('total_revenue')
      ? { isPending: false, isError: false, refetch, ...state }
      : { data: { totals: { visitors: 8 } }, isPending: false, isError: false })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><GoalsPage siteId={7} dateRange={{ from: '2026-10-01', to: '2026-10-07' }} /></QueryClientProvider>)
    expect(await screen.findByText(text)).toBeVisible()
    expect(screen.queryByText('$0.00')).not.toBeInTheDocument()
    if (text !== 'Loading revenue…') {
      fireEvent.click(screen.getByRole('button', { name: 'Retry revenue' }))
      expect(refetch).toHaveBeenCalledOnce()
    }
  })

  it('renders a genuine zero using the configured currency', async () => {
    get.mockReset().mockResolvedValue({ data: [{ id: 4, site_id: 7, kind: 'event', match: 'exact', value: 'purchase', name: 'Purchase', currency: 'EUR' }], response: { status: 200 } })
    useStats.mockReturnValue({ data: { totals: { visitors: 0, total_revenue: 0, average_revenue: 0 } }, isPending: false, isError: false })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><GoalsPage siteId={7} dateRange={{ from: '2026-10-01', to: '2026-10-07' }} /></QueryClientProvider>)
    expect(await screen.findAllByText('€0.00')).toHaveLength(2)
    expect(useStats).toHaveBeenCalledWith(expect.objectContaining({ filters: [
      { dimension: 'goal_id', operator: 'is', values: ['4'] },
      { dimension: 'revenue_currency', operator: 'is', values: ['EUR'] },
    ] }))
  })

  it('does not request revenue for unsupported page or scroll goals', async () => {
    get.mockReset().mockResolvedValue({ data: ['page', 'scroll'].map((kind, id) => ({ id, site_id: 7, kind, match: 'exact', value: '/thanks', name: kind, currency: 'USD' })), response: { status: 200 } })
    useStats.mockReturnValue({ data: { totals: { visitors: 0 } }, isPending: false, isError: false })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><GoalsPage siteId={7} dateRange={{ from: '2026-10-01', to: '2026-10-07' }} /></QueryClientProvider>)
    expect(await screen.findAllByText('Revenue unavailable for this goal')).toHaveLength(2)
    expect(useStats.mock.calls.every(([query]) => !query.metrics.includes('total_revenue'))).toBe(true)
  })

})
