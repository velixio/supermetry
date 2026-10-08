import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

import type { StatsQuery } from '../lib/stats'

const { useStats } = vi.hoisted(() => ({ useStats: vi.fn() }))

vi.mock('../lib/stats', () => ({ useStats }))

import { autoInterval, MainChart } from './MainChart'

beforeAll(() => {
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
  useStats.mockReturnValue(result(rows))
}

describe('MainChart', () => {
  afterEach(cleanup)

  beforeEach(() => {
    vi.clearAllMocks()
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

  it('picks hourly interval for today and daily for 30d', () => {
    expect(autoInterval('2026-10-07', '2026-10-07')).toBe('hour')
    expect(autoInterval('2026-09-08', '2026-10-07')).toBe('day')
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

      expect(useStats).toHaveBeenCalledWith(expect.objectContaining({
        dimensions: ['time'],
        metrics: ['visitors'],
      }))
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
