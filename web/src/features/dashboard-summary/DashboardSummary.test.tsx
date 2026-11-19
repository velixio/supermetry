import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { StatsQuery } from '@/lib/stats'

const { useStats, privateRequest } = vi.hoisted(() => ({ useStats: vi.fn(), privateRequest: vi.fn() }))
vi.mock('@/lib/stats', () => ({ useStats }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { DashboardSummary, type DashboardSummaryProps } from './DashboardSummary'

const query: StatsQuery = {
  siteId: 7,
  dateRange: { from: '2026-11-12', to: '2026-11-18' },
  metrics: ['visitors'],
  filters: [],
  timezone: 'UTC',
  compare: 'previous_period',
}

type Row = Record<string, unknown>
const ok = (totals: Record<string, number>, rows: Row[] = [], comparisonRows?: Row[]) => ({
  data: { columns: [], rows, totals, meta: {}, ...(comparisonRows ? { comparison: { rows: comparisonRows, totals: {}, date_range: query.dateRange } } : {}) },
  isLoading: false,
  isError: false,
})

/** Dispatches like the real hook: the strip's two queries by date range, the cards by dimension. */
function serve(options: { current?: Record<string, number>; previous?: Record<string, number>; sources?: ReturnType<typeof ok>; pages?: ReturnType<typeof ok> } = {}) {
  const current = options.current ?? { visitors: 1240, bounce_rate: 50, visit_duration: 100 }
  const previous = options.previous ?? { visitors: 1050, bounce_rate: 50, visit_duration: 100 }
  useStats.mockImplementation((request: StatsQuery) => {
    if (request.dimensions?.[0] === 'source') return options.sources ?? ok({})
    if (request.dimensions?.[0] === 'page') return options.pages ?? ok({})
    return request.dateRange.from === query.dateRange.from ? ok(current) : ok(previous)
  })
}

function renderSummary(props: Partial<DashboardSummaryProps> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <DashboardSummary anonymous={false} collapsed={false} compare="previous_period" filtered={false} period="7d" query={query} range={query.dateRange} {...props} />
    </QueryClientProvider>,
  )
}

describe('DashboardSummary', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    privateRequest.mockResolvedValue({})
  })
  afterEach(cleanup)

  it('leads with the headline, with the figures emphasised', () => {
    serve()
    renderSummary()
    expect(screen.getByTestId('dashboard-summary-text')).toHaveTextContent('1,240 people visited in the last 7 days, 18.1% more than the 7 days before.')
    expect(screen.getByText('1,240 people')).toHaveClass('font-semibold', 'text-foreground', 'tabular-nums')
    expect(screen.getByTestId('dashboard-summary-text')).toHaveClass('text-foreground')
  })

  it('says so when filters apply, and keeps the delta with comparison off', () => {
    serve()
    renderSummary({ filtered: true, compare: 'off' })
    expect(screen.getByTestId('dashboard-summary-text')).toHaveTextContent('1,240 people visited in the last 7 days matching your filters, 18.1% more than the 7 days before.')
  })

  it('compares with the same dates last year when asked', () => {
    serve()
    renderSummary({ compare: 'previous_year' })
    expect(screen.getByTestId('dashboard-summary-text')).toHaveTextContent('18.1% more than the same dates last year.')
  })

  it('adds the biggest source mover and the top page from the cards queries', () => {
    serve({
      sources: ok({}, [{ source: 'google.com', visitors: 400 }], [{ source: 'google.com', visitors: 300 }]),
      pages: ok({}, [{ page: '/pricing', visitors: 500 }]),
    })
    renderSummary()
    expect(screen.getByTestId('dashboard-summary-text')).toHaveTextContent(/Much of the growth came from google\.com \(\+100 visitors\)\. \/pricing was the most visited page\.$/)
  })

  it('uses the cards\' query shape so the cache entries are shared', () => {
    serve()
    renderSummary()
    const shapes = (useStats.mock.calls as Array<[StatsQuery]>).map(([request]) => [request.dimensions?.[0], request.limit, request.orderBy?.[0]?.column])
    expect(shapes).toContainEqual(['source', 10, 'visitors'])
    expect(shapes).toContainEqual(['page', 10, 'visitors'])
  })

  it('says there is not much traffic below 10 visitors', () => {
    serve({ current: { visitors: 4, bounce_rate: 0, visit_duration: 0 } })
    renderSummary()
    expect(screen.getByTestId('dashboard-summary-text')).toHaveTextContent('Not much traffic yet: 4 visitors in this period.')
  })

  it('shows a one-line skeleton while loading and hides itself on an error', () => {
    useStats.mockReturnValue({ data: undefined, isLoading: true, isError: false })
    const loading = renderSummary()
    expect(screen.getByTestId('dashboard-summary-loading')).toBeInTheDocument()
    loading.unmount()
    useStats.mockReturnValue({ data: undefined, isLoading: false, isError: true })
    renderSummary()
    expect(screen.getByTestId('dashboard-summary')).not.toBeVisible()
  })

  it('asks for no breakdowns in anonymous mode', () => {
    serve()
    renderSummary({ anonymous: true })
    expect(screen.getByTestId('dashboard-summary-text')).toHaveTextContent('1,240 people visited')
    expect((useStats.mock.calls as Array<[StatsQuery]>).every(([request]) => request.dimensions === undefined)).toBe(true)
  })

  it('is not a live region', () => {
    serve()
    renderSummary()
    expect(screen.getByTestId('dashboard-summary').closest('[aria-live]')).toBeNull()
    expect(screen.getByTestId('dashboard-summary')).not.toHaveAttribute('aria-live')
  })

  it('collapses through the remembered preference', async () => {
    serve()
    renderSummary()
    const toggle = screen.getByRole('button', { name: 'Hide summary' })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    fireEvent.click(toggle)
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PATCH', '/api/private/me', { body: { preferences: { summary_collapsed: true } } }))
  })

  it('renders only the toggle when collapsed and expands again', async () => {
    serve()
    renderSummary({ collapsed: true })
    expect(screen.queryByTestId('dashboard-summary-text')).toBeNull()
    expect(useStats).not.toHaveBeenCalled()
    const toggle = screen.getByRole('button', { name: 'Show summary' })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(toggle)
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PATCH', '/api/private/me', { body: { preferences: { summary_collapsed: false } } }))
  })
})
