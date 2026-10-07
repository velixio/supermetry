import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { StatsQuery } from '../lib/stats'

const { useStats } = vi.hoisted(() => ({ useStats: vi.fn() }))

vi.mock('../lib/stats', () => ({ useStats }))

import { autoInterval, MainChart } from './MainChart'

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
})
