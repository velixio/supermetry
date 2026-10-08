import { useState } from 'react'
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { addFilter, dashboardSearch, type Filter } from '@/lib/search'
import type { StatsQuery } from '@/lib/stats'

const { post, refetch, useStats } = vi.hoisted(() => ({
  post: vi.fn(),
  refetch: vi.fn(),
  useStats: vi.fn(),
}))

vi.mock('@/lib/api', () => ({ api: { POST: post } }))
vi.mock('@/lib/stats', () => ({ useStats }))

import { BreakdownCard, type BreakdownTab } from './BreakdownCard'

type QueryData = {
  columns: string[]
  rows: Record<string, unknown>[]
  totals: Record<string, number>
  next_cursor?: string
  meta: { sources_used: string[]; cached: boolean }
}

const baseQuery: StatsQuery = {
  siteId: 7,
  dateRange: { from: '2026-09-08', to: '2026-10-07' },
  metrics: ['visitors'],
  filters: [],
  timezone: 'UTC',
}

const tabs = (dimension: string): BreakdownTab[] => [
  { key: dimension, dimension, metric: 'visitors', label: dimension },
]

function data(dimension: string, value: string, visitors: number): QueryData {
  return {
    columns: [dimension, 'visitors'],
    rows: [{ [dimension]: value, visitors }],
    totals: { visitors },
    meta: { sources_used: ['daily'], cached: false },
  }
}

function result(overrides: Record<string, unknown>) {
  return {
    data: undefined,
    error: null,
    isError: false,
    isLoading: false,
    refetch,
    ...overrides,
  }
}

describe('BreakdownCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    post.mockResolvedValue({ data: 'page,visitors\n/,10\n' })
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('one failing card does not blank the dashboard', () => {
    useStats.mockImplementation((query: StatsQuery) => {
      const dimension = query.dimensions?.[0]
      if (dimension === 'source') {
        return result({
          error: { detail: 'try again', status: 503 },
          isError: true,
        })
      }
      if (dimension === 'page') return result({ data: data('page', '/', 10) })
      return result({ data: data('country', 'IN', 5) })
    })

    render(
      <div>
        <BreakdownCard title="Pages" tabs={tabs('page')} query={baseQuery} onFilter={vi.fn()} />
        <BreakdownCard title="Sources" tabs={tabs('source')} query={baseQuery} onFilter={vi.fn()} />
        <BreakdownCard title="Countries" tabs={tabs('country')} query={baseQuery} onFilter={vi.fn()} />
      </div>,
    )

    expect(screen.getByText('/')).toBeVisible()
    expect(screen.getByText('🇮🇳 India')).toBeVisible()
    const sources = screen.getByRole('region', { name: /sources/i })
    expect(within(sources).getByText(/try again/i)).toBeVisible()
    expect(within(sources).getByRole('button', { name: /retry/i })).toBeEnabled()
  })

  it('clicking a row emits an is filter that round-trips to URL state', () => {
    useStats.mockReturnValue(result({ data: data('country', 'IN', 5) }))

    function FilterHarness() {
      const [search, setSearch] = useState(() => dashboardSearch.parse({}))
      const onFilter = (filter: Filter) => setSearch((current) => addFilter(current, filter))
      return (
        <>
          <BreakdownCard title="Countries" tabs={tabs('country')} query={baseQuery} onFilter={onFilter} />
          <output aria-label="URL filters">{search.f.join(',')}</output>
        </>
      )
    }

    render(<FilterHarness />)
    fireEvent.click(screen.getByRole('button', { name: /filter by country IN/i }))
    expect(screen.getByLabelText('URL filters')).toHaveTextContent('country:is:IN')
  })

  it('long labels truncate with a tooltip and big numbers compact', () => {
    const label = `/${'a'.repeat(300)}`
    useStats.mockReturnValue(result({ data: data('page', label, 12_345_678) }))

    render(
      <div style={{ width: 360 }}>
        <BreakdownCard title="Pages" tabs={tabs('page')} query={baseQuery} onFilter={vi.fn()} />
      </div>,
    )

    expect(screen.getByTitle(label)).toHaveClass('truncate')
    expect(screen.getByText('12.3M')).toBeVisible()
    expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(360)
  })

  it('CSV export requests text/csv with the same query', async () => {
    useStats.mockReturnValue(result({ data: data('page', '/', 10) }))
    render(<BreakdownCard title="Pages" tabs={tabs('page')} query={baseQuery} onFilter={vi.fn()} />)

    fireEvent.click(screen.getByRole('button', { name: /details/i }))
    fireEvent.click(screen.getByRole('button', { name: /csv/i }))

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/private/query', {
        body: {
          site_id: 7,
          date_range: { from: '2026-09-08', to: '2026-10-07' },
          timezone: 'UTC',
          metrics: ['visitors'],
          dimensions: ['page'],
          filters: [],
          include_imported: true,
          order_by: [{ column: 'visitors', direction: 'desc' }],
          limit: 25,
        },
        headers: { Accept: 'text/csv' },
        parseAs: 'text',
      }),
    )
  })
})
