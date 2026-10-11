import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

import type { StatsQuery } from '../lib/stats'

const { useStats, useTimeseries } = vi.hoisted(() => ({ useStats: vi.fn(), useTimeseries: vi.fn() }))

vi.mock('../lib/stats', () => ({ useStats, useTimeseries }))

import { autoInterval, MainChart } from './MainChart'

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn()
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  })
})

const query: StatsQuery = {
  siteId: 7,
  dateRange: { from: '2026-10-01', to: '2026-10-02' },
  metrics: ['visitors'],
  filters: [],
  timezone: 'UTC',
}

function result(rows: Array<Record<string, number | string>>) {
  return {
    data: {
      columns: ['time', 'visitors'],
      rows,
      totals: { visitors: 12 },
      meta: { sources_used: ['daily'], cached: false },
    },
    isLoading: false,
    isError: false,
  }
}

function mockSeries(series: Array<[string, number]>) {
  const rows = series.map(([time, visitors]) => ({ time, visitors }))
  useTimeseries.mockReturnValue(result(rows))
}

describe('MainChart', () => {
  afterEach(cleanup)

  beforeEach(() => {
    vi.clearAllMocks()
    useTimeseries.mockReturnValue(result([]))
  })

  it('shows failed analytics as an error rather than an empty chart', () => {
    useTimeseries.mockReturnValue({ isError: true, isLoading: false, error: { detail: 'Policy denied this chart' }, refetch: vi.fn() })
    render(<MainChart query={query} metric="visitors" compare="off" />)
    expect(screen.getByText('Policy denied this chart')).toBeVisible()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('renders a table fallback with the same numbers', async () => {
    mockSeries([
      ['2026-10-01', 5],
      ['2026-10-02', 7],
    ])

    render(
      <MainChart
        query={query}
        metric="visitors"
        compare="off"
        interval="hour"
      />,
    )

    expect(
      await screen.findByRole('table', { name: /visitors over time/i }),
    ).toHaveTextContent('7')
  })

  it('describes the chart range, total, peak, and low beside its data table', () => {
    mockSeries([
      ['2026-10-01', 5],
      ['2026-10-02', 7],
    ])

    render(<MainChart query={query} metric="visitors" compare="off" interval="day" />)

    const summary = screen.getByText(
      'Visitors over time from Oct 1, 2026 to Oct 2, 2026. Total 12. Peak 7 on Oct 2, 2026. Low 5 on Oct 1, 2026.',
    )
    expect(summary).toHaveClass('sr-only')
    const chart = screen.getByRole('img', { name: 'visitors over time' })
    expect(chart).toHaveAttribute('aria-describedby', summary.id)
    expect(screen.getByRole('table', { name: 'visitors over time' })).toBeInTheDocument()
  })

  it('picks hourly interval for today and daily for 30d', () => {
    expect(autoInterval('2026-10-07', '2026-10-07')).toBe('hour')
    expect(autoInterval('2026-09-08', '2026-10-07')).toBe('day')
  })

  it('requests every chronological hourly bucket for today', () => {
    useTimeseries.mockReturnValue(result([
      { time: '2026-10-01T01:00:00+00:00', visitors: 2 },
      { time: '2026-10-01T00:00:00+00:00', visitors: 1 },
    ]))
    render(<MainChart query={{ ...query, dateRange: { from: '2026-10-01', to: '2026-10-01' } }} metric="visitors" compare="off" />)

    expect(useTimeseries).toHaveBeenCalledWith(expect.objectContaining({
      metrics: ['visitors'],
      dateRange: { from: '2026-10-01', to: '2026-10-01' },
    }), 'hour', true)
    const rows = screen.getAllByRole('row')
    expect(rows[1]).toHaveTextContent('Oct 1, 2026')
    expect(rows[1]).toHaveTextContent('12:00 AM')
    expect(rows[1]).toHaveTextContent('1')
    expect(rows[2]).toHaveTextContent('1:00 AM')
    expect(rows[2]).toHaveTextContent('2')
  })

  it('renders offset-free hourly buckets as site-local wall-clock times', () => {
    useTimeseries.mockReturnValue(result([
      { time: '2026-10-01T01:00:00', visitors: 2 },
    ]))

    render(<MainChart query={{ ...query, timezone: 'America/Los_Angeles', dateRange: { from: '2026-10-01', to: '2026-10-01' } }} metric="visitors" compare="off" />)

    const row = screen.getAllByRole('row')[1]
    expect(row).toHaveTextContent('Oct 1, 2026')
    expect(row).toHaveTextContent('1:00 AM')
    expect(row).not.toHaveTextContent('Sep 30')
  })

  it('lets the reader switch to weekly buckets', () => {
    useTimeseries.mockReturnValue(result([]))
    render(<MainChart query={query} metric="visitors" compare="off" />)

    fireEvent.click(screen.getByRole('combobox', { name: 'Chart interval' }))
    fireEvent.click(screen.getByRole('option', { name: 'Week' }))

    expect(useTimeseries).toHaveBeenCalledWith(expect.anything(), 'week', true)
  })

  it('follows interval prop changes after the chart is mounted', () => {
    const { rerender } = render(<MainChart query={query} metric="visitors" compare="off" interval="day" />)
    expect(screen.getByRole('combobox', { name: 'Chart interval' })).toHaveTextContent('Day')

    rerender(<MainChart query={query} metric="visitors" compare="off" interval="week" />)

    expect(screen.getByRole('combobox', { name: 'Chart interval' })).toHaveTextContent('Week')
    expect(useTimeseries).toHaveBeenCalledWith(expect.anything(), 'week', true)
  })

  it.each(['exit_rate', 'time_on_page'] as const)(
    'falls back from the page-only %s URL metric to a supported chart metric',
    (metric) => {
      mockSeries([['2026-10-01', 5]])

      render(
        <MainChart
          query={query}
          metric={metric}
          compare="off"
        />,
      )

      expect(useTimeseries).toHaveBeenCalledWith(expect.objectContaining({
        metrics: ['visitors'],
      }), expect.any(String), true)
      expect(screen.getByRole('region', { name: 'visitors over time' })).toBeVisible()
    },
  )

  it('orders API rows chronologically', async () => {
    mockSeries([
      ['2026-10-02', 7],
      ['2026-10-01', 5],
    ])

    render(
      <MainChart
        query={query}
        metric="visitors"
        compare="off"
        interval="hour"
      />,
    )

    const rows = await screen.findAllByRole('row')
    expect(rows[1]).toHaveTextContent('Oct 1, 2026')
    expect(rows[2]).toHaveTextContent('Oct 2, 2026')
  })

  it('shows annotated days as flags with their texts and editor actions', async () => {
    mockSeries([
      ['2026-10-01', 5],
      ['2026-10-02', 7],
    ])
    const onEditAnnotation = vi.fn()
    const onDeleteAnnotation = vi.fn()

    render(
      <MainChart
        query={query}
        metric="visitors"
        compare="off"
        annotations={[
          { id: 3, site_id: 7, day: '2026-10-02', text: 'Campaign launched', created_at: '2026-10-02T12:00:00Z' },
          { id: 4, site_id: 7, day: '2026-10-02', text: 'Pricing changed', created_at: '2026-10-02T13:00:00Z' },
        ]}
        canEditAnnotations
        onEditAnnotation={onEditAnnotation}
        onDeleteAnnotation={onDeleteAnnotation}
      />,
    )

    const marker = screen.getByRole('button', { name: '2 annotations on Oct 2, 2026' })
    fireEvent.focus(marker)

    expect(await screen.findByText('Campaign launched')).toBeVisible()
    expect(screen.getByText('Pricing changed')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Edit Campaign launched' }))
    fireEvent.click(screen.getByRole('button', { name: 'Delete Pricing changed' }))
    expect(onEditAnnotation).toHaveBeenCalledWith(expect.objectContaining({ id: 3 }))
    expect(onDeleteAnnotation).toHaveBeenCalledWith(expect.objectContaining({ id: 4 }))
  })
})
