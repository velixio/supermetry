import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { InspectTarget } from '@/lib/inspect'
import type { StatsQuery } from '@/lib/stats'

const { useStats, useBreakdown, refetch } = vi.hoisted(() => ({ useStats: vi.fn(), useBreakdown: vi.fn(), refetch: vi.fn() }))
vi.mock('@/lib/stats', () => ({ useStats, useBreakdown }))

import { Inspector, inspectorTitle } from './Inspector'

const query: StatsQuery = {
  siteId: 7,
  dateRange: { from: '2026-09-28', to: '2026-09-30' },
  metrics: ['visitors'],
  filters: [],
  timezone: 'UTC',
  compare: 'previous_period',
}
const india: InspectTarget = { dimension: 'country', values: ['IN'] }
const statMetrics = ['visitors', 'visits', 'pageviews', 'views_per_visit', 'bounce_rate', 'visit_duration', 'engagement_time']

function ok(data: unknown) {
  return { data, error: null, isError: false, isPending: false, refetch }
}

function totals(visitors: number) {
  return ok({ columns: statMetrics, rows: [], totals: { visitors, visits: 3, pageviews: 9, views_per_visit: 3, bounce_rate: 50, visit_duration: 60, engagement_time: 30 }, meta: { sources_used: ['daily'], cached: false } })
}

function rows(dimension: string, value: string, visitors: number) {
  return ok({ columns: [dimension, 'visitors'], rows: [{ [dimension]: value, visitors }], totals: {}, meta: { sources_used: ['raw'], cached: false } })
}

beforeEach(() => {
  vi.clearAllMocks()
  useStats.mockReturnValue(totals(42))
  useBreakdown.mockImplementation((request: StatsQuery) => rows(request.dimensions?.[0] ?? 'page', 'Example', 7))
})

afterEach(cleanup)

describe('Inspector', () => {
  it('shows the value metrics from the stats-strip query with the value added as a filter', () => {
    render(<Inspector anonymous={false} query={query} target={india} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(useStats).toHaveBeenCalledWith(expect.objectContaining({
      metrics: statMetrics,
      dimensions: undefined,
      compare: undefined,
      filters: [{ dimension: 'country', operator: 'is', values: ['IN'] }],
    }))
    const metrics = screen.getByRole('region', { name: 'Metrics for this value' })
    expect(metrics).toHaveTextContent('Visitors')
    expect(metrics).toHaveTextContent('42')
    expect(metrics).toHaveTextContent('Bounce Rate')
  })

  it('cross-breaks the value by the other supported dimensions, five rows each', () => {
    render(<Inspector anonymous={false} query={query} target={india} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(useBreakdown.mock.calls.map(([request]) => request.dimensions)).toEqual([['page'], ['source'], ['device']])
    for (const [request] of useBreakdown.mock.calls) {
      expect(request).toMatchObject({ metrics: ['visitors'], limit: 5, compare: undefined, filters: [{ dimension: 'country', operator: 'is', values: ['IN'] }] })
    }
    expect(screen.getByRole('region', { name: 'Page: top 5' })).toHaveTextContent('Example7')
  })

  it('limits anonymous cross-breakdowns to safe dimensions', () => {
    render(<Inspector anonymous query={query} target={india} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(useBreakdown.mock.calls.map(([request]) => request.dimensions)).toEqual([['channel'], ['device']])
  })

  it('scopes a city to its country and skips the country breakdown', () => {
    render(<Inspector anonymous={false} query={query} target={{ dimension: 'city', values: ['Springfield', 'US'] }} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(useStats).toHaveBeenCalledWith(expect.objectContaining({ filters: [{ dimension: 'city', operator: 'is', values: ['Springfield', 'US'] }] }))
    expect(useBreakdown.mock.calls.map(([request]) => request.dimensions)).toEqual([['page'], ['source'], ['device']])
  })

  it('says Out of range instead of closing when the filters exclude the value', () => {
    useStats.mockReturnValue(totals(0))
    render(<Inspector anonymous={false} query={query} target={india} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(screen.getByRole('status')).toHaveTextContent('Out of range')
    expect(screen.queryByRole('region', { name: 'Metrics for this value' })).toBeNull()
  })

  it('adds the value as a filter, and says so once it is one', () => {
    const onFilter = vi.fn()
    const { rerender } = render(<Inspector anonymous={false} query={query} target={india} trafficMetric="visitors" onFilter={onFilter} />)
    fireEvent.click(screen.getByRole('button', { name: 'Add as filter' }))
    expect(onFilter).toHaveBeenCalledWith({ dimension: 'country', operator: 'is', values: ['IN'] })

    useStats.mockClear()
    const filtered = { ...query, filters: [{ dimension: 'country', operator: 'is' as const, values: ['IN'] }] }
    rerender(<Inspector anonymous={false} query={filtered} target={india} trafficMetric="visitors" onFilter={onFilter} />)
    expect(screen.queryByRole('button', { name: 'Add as filter' })).toBeNull()
    expect(screen.getByText('This value is already a filter.')).toBeVisible()
    expect(useStats).toHaveBeenLastCalledWith(expect.objectContaining({ filters: filtered.filters }))
  })

  it('sends nothing and explains when ten filters are already applied', () => {
    const full = { ...query, filters: Array.from({ length: 10 }, (_, index) => ({ dimension: 'page', operator: 'is_not' as const, values: [`/${index}`] })) }
    render(<Inspector anonymous={false} query={full} target={india} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(screen.getByRole('status')).toHaveTextContent('queries allow at most 10 filters')
    expect(useStats).not.toHaveBeenCalled()
    expect(useBreakdown).not.toHaveBeenCalled()
  })

  it('shows a retryable error when the metrics fail', () => {
    useStats.mockReturnValue({ data: undefined, error: { detail: 'Upstream failed' }, isError: true, isPending: false, refetch })
    render(<Inspector anonymous={false} query={query} target={india} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(screen.getByRole('alert')).toHaveTextContent('Upstream failed')
  })

  it('reads a Weekly rhythm cell from the cached grid query and offers no filter', () => {
    useBreakdown.mockReturnValue(ok({ columns: ['weekday', 'hour_of_day', 'pageviews'], rows: [{ weekday: 1, hour_of_day: 13, pageviews: 178 }], totals: {}, meta: { sources_used: ['raw'], cached: false } }))
    render(<Inspector anonymous={false} query={query} target={{ dimension: 'weekday_hour', values: ['1', '13'] }} trafficMetric="pageviews" onFilter={vi.fn()} />)

    expect(useBreakdown).toHaveBeenCalledWith({ ...query, metrics: ['pageviews'], dimensions: ['weekday', 'hour_of_day'], limit: 168 })
    expect(useStats).not.toHaveBeenCalled()
    expect(screen.getByText('178')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Add as filter' })).toBeNull()
  })

  it('says Out of range for a Weekly rhythm cell with no traffic', () => {
    useBreakdown.mockReturnValue(ok({ columns: [], rows: [], totals: {}, meta: { sources_used: ['raw'], cached: false } }))
    render(<Inspector anonymous={false} query={query} target={{ dimension: 'weekday_hour', values: ['0', '3'] }} trafficMetric="visitors" onFilter={vi.fn()} />)

    expect(screen.getByRole('status')).toHaveTextContent('Out of range')
  })

  it('titles a value the way the cards label it', () => {
    expect(inspectorTitle(india)).toBe('India')
    expect(inspectorTitle({ dimension: 'city', values: ['Springfield', 'US'] })).toBe('Springfield, United States')
    expect(inspectorTitle({ dimension: 'screen_size', values: ['mobile'] })).toBe('Mobile (<768px)')
    expect(inspectorTitle({ dimension: 'weekday_hour', values: ['1', '13'] })).toBe('Monday 1 PM')
  })
})
