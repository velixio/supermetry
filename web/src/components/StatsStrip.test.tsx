import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { StatsQuery } from '../lib/stats'

const { useStats } = vi.hoisted(() => ({ useStats: vi.fn() }))

vi.mock('../lib/stats', () => ({ useStats }))

import { StatsStrip } from './StatsStrip'

const query: StatsQuery = {
  siteId: 7,
  dateRange: { from: '2026-10-01', to: '2026-10-07' },
  metrics: ['visitors'],
  filters: [],
  timezone: 'UTC',
}

function result(totals: Record<string, number>, importedIncluded = false) {
  return {
    data: {
      columns: Object.keys(totals),
      rows: [],
      totals,
      meta: { sources_used: ['daily'], cached: false, imported_included: importedIncluded },
    },
    isLoading: false,
    isError: false,
  }
}

function mockQuery(
  current: Record<string, number>,
  comparison: Record<string, number>,
) {
  useStats
    .mockReturnValueOnce(result(current))
    .mockReturnValueOnce(result(comparison))
}

describe('StatsStrip', () => {
  afterEach(cleanup)

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows a query error instead of zero totals', () => {
    useStats.mockReturnValue({ isError: true, isLoading: false, error: { detail: 'Policy denied this query' }, refetch: vi.fn() })
    render(<StatsStrip query={query} compare="off" onSelectMetric={vi.fn()} />)
    expect(screen.getByText('Policy denied this query')).toBeVisible()
    expect(screen.queryByRole('button', { name: /^Visitors/ })).not.toBeInTheDocument()
  })

  it('shows deltas with arrows and good/bad color', async () => {
    mockQuery(
      { visitors: 110, bounce_rate: 40 },
      { visitors: 100, bounce_rate: 50 },
    )

    render(
      <StatsStrip
        query={query}
        compare="previous_period"
        onSelectMetric={vi.fn()}
      />,
    )

    expect(await screen.findByText('▲ 10%')).toHaveAttribute(
      'data-trend',
      'good',
    )
    expect(screen.getByText('▼ 20%')).toHaveAttribute(
      'data-trend',
      'good',
    )
    expect(useStats).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        dateRange: { from: '2026-09-24', to: '2026-09-30' },
      }),
    )
  })

  it('selects the clicked metric', () => {
    const onSelectMetric = vi.fn()
    mockQuery({ visitors: 110 }, { visitors: 100 })

    render(
      <StatsStrip
        query={query}
        compare="previous_period"
        onSelectMetric={onSelectMetric}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: /visitors/i }))
    expect(onSelectMetric).toHaveBeenCalledWith('visitors')
  })

  it('shows an em dash for empty or zero engagement time', () => {
    mockQuery({ engagement_time: 0 }, {})
    render(
      <StatsStrip query={query} compare="off" onSelectMetric={vi.fn()} />,
    )

    const engagement = screen.getByRole('button', { name: /engagement time/i })
    expect(engagement).toHaveTextContent('—')
    expect(engagement).not.toHaveTextContent('0s')
  })

  it('marks summaries that include imported data', () => {
    useStats
      .mockReturnValueOnce(result({ visitors: 110 }, true))
      .mockReturnValueOnce(result({ visitors: 100 }))

    render(<StatsStrip query={query} compare="off" onSelectMetric={vi.fn()} />)

    expect(screen.getByText('Includes imported data')).toBeVisible()
  })
})
