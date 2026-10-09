import { renderAuthorized as render } from '@/test/authorization'
import { useState } from 'react'
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { addFilter, dashboardSearch, type Filter } from '@/lib/search'
import type { StatsQuery } from '@/lib/stats'

const { post, refetch, useStats, worldMapLoads } = vi.hoisted(() => ({
  post: vi.fn(),
  refetch: vi.fn(),
  useStats: vi.fn(),
  worldMapLoads: { value: 0 },
}))

vi.mock('@/lib/api', () => ({ api: { POST: post } }))
vi.mock('@/lib/stats', () => ({ useStats }))
vi.mock('./WorldMap', () => {
  worldMapLoads.value += 1
  return {
    WorldMap: ({ rows, onSelect }: { rows: { country: string; visitors: number }[]; onSelect(code: string): void }) => (
      <svg aria-label="World map" role="group">
        {rows.map((row) => (
          <path
            key={row.country}
            aria-label={`India: ${row.visitors} visitors`}
            role="button"
            onClick={() => onSelect(row.country)}
          />
        ))}
      </svg>
    ),
  }
})

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

const pageTabs = [{
  key: 'top',
  dimension: 'page',
  metric: 'visitors',
  metrics: ['visitors', 'pageviews', 'time_on_page', 'exit_rate'],
  label: 'Top',
}] as unknown as BreakdownTab[]

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
    expect(screen.getByText('India')).toBeVisible()
    expect(screen.getByRole('presentation', { hidden: true })).toHaveAttribute('src', '/flags/in.svg')
    const sources = screen.getByRole('region', { name: /sources/i })
    expect(within(sources).getByText(/try again/i)).toBeVisible()
    expect(within(sources).getByRole('button', { name: /retry/i })).toBeEnabled()
  })

  it('queries country with region and displays its flag without changing the raw filter', () => {
    useStats.mockReturnValue(result({
      data: {
        ...data('region', 'California', 5),
        columns: ['country', 'region', 'visitors'],
        rows: [{ country: 'US', region: 'California', visitors: 5 }],
      },
    }))
    const onFilter = vi.fn()

    render(<BreakdownCard title="Countries" tabs={tabs('region')} query={baseQuery} onFilter={onFilter} />)

    expect(useStats).toHaveBeenCalledWith(expect.objectContaining({ dimensions: ['region', 'country'] }))
    expect(screen.getByRole('presentation', { hidden: true })).toHaveAttribute('src', '/flags/us.svg')
    fireEvent.click(screen.getByRole('button', { name: /filter by region California/i }))
    expect(onFilter).toHaveBeenCalledWith({ dimension: 'region', operator: 'is', values: ['California'] })
  })

  it('queries country with region in the details table', () => {
    useStats.mockReturnValue(result({
      data: {
        ...data('region', 'California', 5),
        columns: ['country', 'region', 'visitors'],
        rows: [{ country: 'US', region: 'California', visitors: 5 }],
      },
    }))

    render(<BreakdownCard title="Countries" tabs={tabs('region')} query={baseQuery} onFilter={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: 'Details' }))

    expect(useStats).toHaveBeenLastCalledWith(expect.objectContaining({ dimensions: ['region', 'country'] }))
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

  it('renders an empty dimension as Unknown without a filter action', () => {
    useStats.mockReturnValue(result({ data: data('browser_version', '', 5) }))
    const onFilter = vi.fn()

    render(
      <BreakdownCard
        title="Devices"
        tabs={tabs('browser_version')}
        query={baseQuery}
        onFilter={onFilter}
      />,
    )

    expect(screen.getByText('Unknown')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: /filter by browser_version/i }),
    ).not.toBeInTheDocument()
    expect(onFilter).not.toHaveBeenCalled()
  })

  it.each([
    ['mobile', 'Mobile (<768px)'],
    ['tablet', 'Tablet (768–1023px)'],
    ['laptop', 'Laptop (1024–1439px)'],
    ['desktop', 'Desktop (1440–1919px)'],
    ['large', 'Large (≥1920px)'],
  ])('renders the %s screen bucket as %s', (value, label) => {
    useStats.mockReturnValue(result({ data: data('screen_size', value, 5) }))

    render(
      <BreakdownCard
        title="Devices"
        tabs={tabs('screen_size')}
        query={baseQuery}
        onFilter={vi.fn()}
      />,
    )

    expect(screen.getByText(label)).toBeVisible()
    expect(screen.getByRole('button', { name: `Filter by screen_size ${value}` })).toBeEnabled()
  })

  it('defaults a map-enabled country breakdown to its list and switches to the lazy map', async () => {
    useStats.mockReturnValue(result({ data: data('country', 'IN', 5) }))

    render(
      <BreakdownCard
        title="Countries"
        tabs={tabs('country')}
        query={baseQuery}
        onFilter={vi.fn()}
        map
      />,
    )

    expect(screen.getByRole('list')).toBeVisible()
    expect(screen.queryByRole('group', { name: /world map/i })).not.toBeInTheDocument()
    expect(worldMapLoads.value).toBe(0)

    fireEvent.click(screen.getByRole('button', { name: 'Map' }))

    expect(await screen.findByRole('group', { name: /world map/i })).toBeVisible()
    expect(worldMapLoads.value).toBe(1)
    expect(useStats).toHaveBeenLastCalledWith(expect.objectContaining({ limit: 250 }))
  })

  it('selecting a country on the map emits the same country filter as a list row', async () => {
    useStats.mockReturnValue(result({ data: data('country', 'IN', 5) }))
    const onFilter = vi.fn()

    render(
      <BreakdownCard
        title="Countries"
        tabs={tabs('country')}
        query={baseQuery}
        onFilter={onFilter}
        map
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Map' }))
    fireEvent.click(await screen.findByRole('button', { name: 'India: 5 visitors' }))

    expect(onFilter).toHaveBeenCalledWith({ dimension: 'country', operator: 'is', values: ['IN'] })
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

  it('shows time on page and exit rate columns with undefined values last', () => {
    useStats.mockReturnValue(result({
      data: {
        columns: ['page', 'visitors', 'pageviews', 'time_on_page', 'exit_rate'],
        rows: [
          { page: '/engaged', visitors: 9, pageviews: 3, time_on_page: 65, exit_rate: 33.3 },
          { page: '/zero-exits', visitors: 4, pageviews: 4, time_on_page: 0, exit_rate: 0 },
          { page: '/no-data', visitors: 0, pageviews: 0, time_on_page: 0, exit_rate: 0 },
        ],
        totals: { visitors: 13, pageviews: 7, time_on_page: 65, exit_rate: 14.3 },
        meta: { sources_used: ['daily'], cached: false },
      },
    }))

    render(<BreakdownCard title="Pages" tabs={pageTabs} query={baseQuery} onFilter={vi.fn()} />)

    const card = screen.getByRole('region', { name: 'Pages' })
    expect(useStats).toHaveBeenCalledWith(expect.objectContaining({
      metrics: ['visitors', 'pageviews', 'time_on_page', 'exit_rate'],
    }))
    expect(within(card).getByRole('columnheader', { name: 'Time on page' })).toBeVisible()
    expect(within(card).getByRole('columnheader', { name: 'Exit rate' })).toBeVisible()
    expect(within(card).getByText('1m 05s')).toBeVisible()
    expect(within(card).getByText('33.3%')).toBeVisible()

    const zeroExits = within(card).getByText('/zero-exits').closest('tr')
    expect(zeroExits).not.toBeNull()
    expect(within(zeroExits!).getByText('0%')).toBeVisible()
    expect(within(zeroExits!).getByText('—')).toBeVisible()

    const noData = within(card).getByText('/no-data').closest('tr')
    expect(noData).not.toBeNull()
    expect(within(noData!).getAllByText('—')).toHaveLength(2)
    expect(within(card).getAllByRole('row').at(-1)).toHaveTextContent('/no-data')
  })

  it('shows and sorts the page metrics in the details table', () => {
    useStats.mockReturnValue(result({
      data: {
        columns: ['page', 'visitors', 'pageviews', 'time_on_page', 'exit_rate'],
        rows: [
          { page: '/no-data', visitors: 0, pageviews: 0, time_on_page: 0, exit_rate: 0 },
          { page: '/engaged', visitors: 9, pageviews: 3, time_on_page: 12, exit_rate: 33.3 },
        ],
        totals: { visitors: 9, pageviews: 3, time_on_page: 12, exit_rate: 33.3 },
        meta: { sources_used: ['daily'], cached: false },
      },
    }))

    render(<BreakdownCard title="Pages" tabs={pageTabs} query={baseQuery} onFilter={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: 'Details' }))

    const table = screen.getByRole('table', { name: 'Pages details' })
    expect(within(table).getByRole('columnheader', { name: 'Time on page' })).toBeVisible()
    expect(within(table).getByRole('columnheader', { name: 'Exit rate' })).toBeVisible()
    expect(within(table).getByText('12s')).toBeVisible()

    fireEvent.click(within(table).getByRole('button', { name: 'Time on page' }))
    expect(useStats).toHaveBeenLastCalledWith(expect.objectContaining({
      orderBy: [{ column: 'time_on_page', direction: 'desc' }],
    }))
    expect(within(table).getAllByRole('row').at(-1)).toHaveTextContent('/no-data')

    fireEvent.click(within(table).getByRole('button', { name: /Time on page/ }))
    expect(useStats).toHaveBeenLastCalledWith(expect.objectContaining({
      orderBy: [{ column: 'time_on_page', direction: 'desc' }],
    }))

    fireEvent.click(within(table).getByRole('button', { name: 'Exit rate' }))
    expect(useStats).toHaveBeenLastCalledWith(expect.objectContaining({
      orderBy: [
        { column: 'exit_rate', direction: 'desc' },
        { column: 'pageviews', direction: 'desc' },
      ],
    }))
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
        params: {
          query: {
            format: 'csv',
            limit: 'all',
          },
        },
        parseAs: 'text',
      }),
    )
  })
})
