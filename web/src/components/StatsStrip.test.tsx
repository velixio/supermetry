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

  it('shows signed deltas with arrows and good/bad color', async () => {
    mockQuery(
      { visitors: 110, bounce_rate: 40 },
      { visitors: 100, bounce_rate: 50 },
    )

    render(<StatsStrip query={query} compare="previous_period" onSelectMetric={vi.fn()} />)

    const up = await screen.findByText('+10%')
    expect(up).toHaveAttribute('data-trend', 'good')
    expect(up.querySelector('svg')).not.toBeNull()
    // A falling bounce rate is good news.
    const down = screen.getByText('\u221220%')
    expect(down).toHaveAttribute('data-trend', 'good')
    expect(useStats).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        dateRange: { from: '2026-09-24', to: '2026-09-30' },
      }),
    )
  })

  it('computes the delta against the previous period even with comparison off', () => {
    mockQuery({ visitors: 90 }, { visitors: 100 })
    render(<StatsStrip query={query} compare="off" selected="visitors" onSelectMetric={vi.fn()} />)

    expect(screen.getByText('\u221210%')).toHaveAttribute('data-trend', 'bad')
    expect(useStats).toHaveBeenNthCalledWith(2, expect.objectContaining({ dateRange: { from: '2026-09-24', to: '2026-09-30' } }))
    expect(screen.getByText('\u221210%')).toHaveAttribute('title', 'vs previous 7 days')
  })

  it('compares with the same dates last year when that comparison is chosen', () => {
    mockQuery({ visitors: 120 }, { visitors: 100 })
    render(<StatsStrip query={query} compare="previous_year" onSelectMetric={vi.fn()} />)

    expect(useStats).toHaveBeenNthCalledWith(2, expect.objectContaining({ dateRange: { from: '2025-10-01', to: '2025-10-07' } }))
    expect(screen.getByRole('button', { name: /visitors/i })).toHaveAccessibleName('Visitors 120, up 20% vs same dates last year')
  })

  it('says the direction and size of the change in the card name', () => {
    mockQuery({ visitors: 110, bounce_rate: 60, visits: 5 }, { visitors: 100, bounce_rate: 50, visits: 5 })
    render(<StatsStrip query={query} compare="off" onSelectMetric={vi.fn()} />)

    expect(screen.getByRole('button', { name: /^Visitors/ })).toHaveAccessibleName('Visitors 110, up 10% vs previous 7 days')
    expect(screen.getByRole('button', { name: /^Bounce rate/ })).toHaveAccessibleName('Bounce rate 60%, up 20% vs previous 7 days')
    expect(screen.getByRole('button', { name: /^Visits/ })).toHaveAccessibleName('Visits 5, unchanged vs previous 7 days')
  })

  it('shows no change when the previous period had no data', () => {
    mockQuery({ visitors: 10 }, { visitors: 0 })
    render(<StatsStrip query={query} compare="off" onSelectMetric={vi.fn()} />)
    expect(screen.getByRole('button', { name: /^Visitors/ })).toHaveAccessibleName('Visitors 10, no previous data to compare')
  })

  it('marks the card the chart shows as pressed', () => {
    mockQuery({ visitors: 110, pageviews: 240 }, {})
    render(<StatsStrip query={query} compare="off" selected="pageviews" onSelectMetric={vi.fn()} />)

    expect(screen.getByRole('button', { name: /^Pageviews/ })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: /^Visitors/ })).toHaveAttribute('aria-pressed', 'false')
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

  it('keeps views per visit and engagement time behind More metrics', () => {
    useStats.mockReturnValue(result({ views_per_visit: 2, engagement_time: 0 }))
    render(<StatsStrip query={query} compare="off" selected="visitors" onSelectMetric={vi.fn()} />)

    expect(screen.getAllByRole('button', { name: /^(Visitors|Visits|Pageviews|Bounce rate|Visit duration)/ })).toHaveLength(5)
    expect(screen.queryByRole('button', { name: /engagement time/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /views per visit/i })).not.toBeInTheDocument()

    const more = screen.getByRole('button', { name: 'More metrics' })
    expect(more).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(more)
    expect(screen.getByRole('button', { name: 'Fewer metrics' })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('button', { name: /views per visit/i })).toBeVisible()
  })

  it('shows an em dash for empty or zero engagement time', () => {
    mockQuery({ engagement_time: 0 }, {})
    render(<StatsStrip query={query} compare="off" selected="engagement_time" onSelectMetric={vi.fn()} />)

    const engagement = screen.getByRole('button', { name: /engagement time/i })
    expect(engagement).toHaveTextContent('\u2014')
    expect(engagement).not.toHaveTextContent('0s')
  })

  it('opens More metrics when a hidden metric is the selected one', () => {
    mockQuery({ engagement_time: 12 }, {})
    render(<StatsStrip query={query} compare="off" selected="engagement_time" onSelectMetric={vi.fn()} />)
    expect(screen.getByRole('button', { name: /^Engagement time/ })).toHaveAttribute('aria-pressed', 'true')
  })

  it('marks summaries that include imported data', () => {
    useStats
      .mockReturnValueOnce(result({ visitors: 110 }, true))
      .mockReturnValueOnce(result({ visitors: 100 }))

    render(<StatsStrip query={query} compare="off" onSelectMetric={vi.fn()} />)

    expect(screen.getByText('Includes imported data')).toBeVisible()
  })

  it('shows no imported-data chip when no imported data overlaps the range', () => {
    mockQuery({ visitors: 110 }, { visitors: 100 })
    render(<StatsStrip query={query} compare="off" onSelectMetric={vi.fn()} />)
    expect(screen.queryByText('Includes imported data')).not.toBeInTheDocument()
  })

  it('lists only the requested tiles for a widget, all visible', () => {
    mockQuery({ visitors: 1, views_per_visit: 3 }, {})
    render(<StatsStrip query={query} compare="off" metrics={['visitors', 'views_per_visit']} />)
    expect(screen.getByText('Views per visit')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'More metrics' })).not.toBeInTheDocument()
  })
})
