import '@testing-library/jest-dom/vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { useStats } = vi.hoisted(() => ({ useStats: vi.fn() }))
vi.mock('@/lib/stats', () => ({ useStats }))

import { RevenueReport } from './RevenueReport'

const base = {
  siteId: 7,
  currency: 'USD',
  timezone: 'America/New_York',
  dateRange: { from: '2026-03-07', to: '2026-03-09' },
  filters: [],
  compare: 'previous_period' as const,
  includeImported: true,
}

function response(dimension?: string) {
  const rows = dimension === 'time'
    ? [{ time: '2026-03-07', total_revenue: 40 }, { time: '2026-03-08', total_revenue: 60 }]
    : [{ [dimension ?? 'source']: 'Newsletter', total_revenue: 100, average_revenue: 25 }]
  return {
    rows,
    totals: { total_revenue: 100, average_revenue: 25 },
    meta: { sources_used: ['hot'], cached: false, imported_included: true, ...(dimension === 'time' ? {} : { event_attribution: { basis: 'event', coverage: 'exact', legacy_excluded: false } }) },
    comparison: { date_range: { from: '2026-03-04', to: '2026-03-06' }, rows: [], totals: { total_revenue: 80, average_revenue: 20 }, ...(dimension === 'time' ? {} : { event_attribution: { basis: 'event', coverage: 'exact', legacy_excluded: false } }) },
  }
}

describe('RevenueReport', () => {
  beforeEach(() => {
    useStats.mockImplementation((query: { dimensions?: string[] }) => ({
      data: response(query.dimensions?.[0]), isPending: false, isError: false, refetch: vi.fn(),
    }))
  })
  afterEach(cleanup)

  it('runs one bounded trend and three event-attribution breakdown queries', () => {
    render(<RevenueReport {...base} />)
    expect(useStats).toHaveBeenCalledTimes(4)
    expect(useStats).toHaveBeenCalledWith(expect.objectContaining({
      dimensions: ['time'], metrics: ['total_revenue', 'average_revenue'], timezone: 'America/New_York', compare: 'previous_period', limit: 400,
    }))
    for (const dimension of ['source', 'channel', 'utm_campaign']) {
      expect(useStats).toHaveBeenCalledWith(expect.objectContaining({
        dimensions: [dimension], orderBy: [{ column: 'total_revenue', direction: 'desc' }], limit: 10,
      }))
    }
  })

  it('formats currency, comparisons, trend, and breakdown values', () => {
    render(<RevenueReport {...base} />)
    expect(screen.getByRole('heading', { name: 'Revenue' })).toBeVisible()
    expect(screen.getAllByText('$100.00')).toHaveLength(4)
    expect(screen.getAllByText('+25% vs previous period')).toHaveLength(2)
    expect(screen.getByRole('table', { name: 'Revenue over time' })).toHaveTextContent('Mar 8, 2026$60.00')
    expect(screen.getAllByText('Newsletter')).toHaveLength(3)
    expect(screen.queryByText(/available attributed revenue/i)).not.toBeInTheDocument()
  })

  it('labels incomplete legacy attribution without reducing the full trend total', () => {
    useStats.mockImplementation((query: { dimensions?: string[] }) => {
      const data = response(query.dimensions?.[0])
      if (query.dimensions?.[0] !== 'time') {
        data.totals.total_revenue = 70
        data.meta.event_attribution = { basis: 'event', coverage: 'available_only', legacy_excluded: true }
      }
      return { data, isPending: false, isError: false, refetch: vi.fn() }
    })
    render(<RevenueReport {...base} />)
    expect(screen.getAllByText('$100.00')).not.toHaveLength(0)
    expect(screen.getAllByText(/available attributed revenue/i)).toHaveLength(3)
  })

  it('labels headline totals when selected attribution filters make the trend available-only', () => {
    useStats.mockImplementation((query: { dimensions?: string[] }) => {
      const data = response(query.dimensions?.[0])
      if (query.dimensions?.[0] === 'time') data.meta.event_attribution = { basis: 'event', coverage: 'available_only', legacy_excluded: true }
      return { data, isPending: false, isError: false, refetch: vi.fn() }
    })
    render(<RevenueReport {...base} filters={[{ dimension: 'source', operator: 'is', values: ['Newsletter'] }]} />)
    expect(screen.getByText(/selected attribution filters exclude legacy/i)).toBeVisible()
  })

  it('renders genuine zero and a retryable error', () => {
    useStats.mockImplementation((query: { dimensions?: string[] }) => query.dimensions?.[0] === 'time'
      ? { data: { ...response('time'), totals: { total_revenue: 0, average_revenue: 0 } }, isPending: false, isError: false, refetch: vi.fn() }
      : { isPending: false, isError: true, error: { detail: 'query failed' }, refetch: vi.fn() })
    render(<RevenueReport {...base} currency="EUR" compare="off" />)
    expect(screen.getAllByText('€0.00')).toHaveLength(2)
    expect(screen.getAllByText('query failed')).toHaveLength(3)
  })
})
