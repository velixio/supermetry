import { renderAuthorized as render } from '@/test/authorization'
import { useState } from 'react'
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
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

  it('does not render unverified page labels or false zeros as verified analytics', () => {
    useStats.mockReturnValue(result({ data: data('page', '/private', 99) }))
    render(<BreakdownCard title="Verified pages" tabs={tabs('page')} query={baseQuery} onFilter={vi.fn()} verifiedPaths allowFilter={false} />)
    expect(screen.getByText(/Verified page coverage could not be confirmed/)).toBeVisible()
    expect(screen.queryByText('/private')).not.toBeInTheDocument()
    expect(screen.queryByText('99')).not.toBeInTheDocument()
  })

  it('keeps canonical detail search disabled in anonymous mode', async () => {
    useStats.mockReturnValue(result({ data: data('country', 'US', 2) }))
    render(<BreakdownCard title="Countries" tabs={tabs('country')} query={baseQuery} onFilter={vi.fn()} allowSearch={false} />)
    fireEvent.click(screen.getByRole('button', { name: /Details/i }))
    expect(await screen.findByRole('searchbox', { name: 'Search country' })).toBeDisabled()
    expect(screen.getByText(/Free-text searches are unavailable/)).toBeVisible()
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

  it('scopes a region filter to its country', () => {
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
    const region = screen.getByRole('button', { name: 'California5' })
    expect(region).toHaveAccessibleDescription('Filter by region California, United States')
    fireEvent.click(region)
    expect(onFilter).toHaveBeenCalledWith({ dimension: 'region', operator: 'is', values: ['California', 'US'] })
  })

  it('keeps identically named cities in different countries separate', () => {
    useStats.mockReturnValue(result({
      data: {
        columns: ['country', 'city', 'visitors'],
        rows: [
          { country: 'US', city: 'Springfield', visitors: 5 },
          { country: 'CA', city: 'Springfield', visitors: 3 },
        ],
        totals: { visitors: 8 },
        meta: { sources_used: ['closed'], cached: false },
      },
    }))
    const onFilter = vi.fn()

    render(<BreakdownCard title="Cities" tabs={tabs('city')} query={baseQuery} onFilter={onFilter} />)

    const usCity = screen.getByRole('button', { name: 'Springfield5' })
    const caCity = screen.getByRole('button', { name: 'Springfield3' })
    expect(usCity).toHaveAccessibleDescription('Filter by city Springfield, United States')
    expect(caCity).toHaveAccessibleDescription('Filter by city Springfield, Canada')
    fireEvent.click(usCity)
    fireEvent.click(caCity)
    expect(onFilter).toHaveBeenNthCalledWith(1, { dimension: 'city', operator: 'is', values: ['Springfield', 'US'] })
    expect(onFilter).toHaveBeenNthCalledWith(2, { dimension: 'city', operator: 'is', values: ['Springfield', 'CA'] })
  })

  it('does not offer an invalid place filter when its country is unknown', () => {
    useStats.mockReturnValue(result({ data: data('city', 'Springfield', 5) }))
    const onFilter = vi.fn()

    render(<BreakdownCard title="Cities" tabs={tabs('city')} query={baseQuery} onFilter={onFilter} />)

    expect(screen.queryByRole('button', { name: /filter by city Springfield/i })).not.toBeInTheDocument()
    expect(screen.getByText('Springfield')).toBeVisible()
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
    const country = screen.getByRole('button', { name: 'India5' })
    expect(country).toHaveAccessibleDescription('Filter by country India')
    fireEvent.click(country)
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
    expect(screen.getByText('Unknown').closest('[title="Unknown"]')).toHaveClass('text-foreground')
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
    const filter = screen.getByRole('button', { name: `${label}5` })
    expect(filter).toHaveAccessibleDescription(`Filter by screen_size ${value}`)
    expect(filter).toBeEnabled()
  })

  it('keeps the Location country breakdown as a list because Where owns the map', () => {
    useStats.mockReturnValue(result({ data: data('country', 'IN', 5) }))

    render(
      <BreakdownCard
        title="Location"
        tabs={tabs('country')}
        query={baseQuery}
        onFilter={vi.fn()}
      />,
    )

    expect(screen.getByRole('list')).toBeVisible()
    expect(screen.queryByRole('group', { name: /world map/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Map' })).not.toBeInTheDocument()
    expect(useStats).toHaveBeenLastCalledWith(expect.objectContaining({ limit: 10 }))
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

  it('offers a separate Inspect control that does not apply the filter', () => {
    useStats.mockReturnValue(result({ data: data('country', 'IN', 5) }))
    const onFilter = vi.fn()
    const open = vi.fn()
    render(<BreakdownCard title="Countries" tabs={tabs('country')} query={baseQuery} onFilter={onFilter} inspect={{ can: () => true, open }} />)

    const inspect = screen.getByRole('button', { name: 'Inspect India' })
    expect(inspect).toHaveAttribute('aria-pressed', 'false')
    fireEvent.click(inspect)
    expect(open).toHaveBeenCalledWith({ dimension: 'country', values: ['IN'] }, inspect)
    expect(onFilter).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'India5' })).toHaveAccessibleDescription('Filter by country India')
  })

  it('marks the inspected place and scopes it to its country', () => {
    useStats.mockReturnValue(result({
      data: { ...data('region', 'California', 5), columns: ['country', 'region', 'visitors'], rows: [{ country: 'US', region: 'California', visitors: 5 }] },
    }))
    render(<BreakdownCard title="Regions" tabs={tabs('region')} query={baseQuery} onFilter={vi.fn()} inspect={{ selected: { dimension: 'region', values: ['California', 'US'] }, can: () => true, open: vi.fn() }} />)

    expect(screen.getByRole('button', { name: 'Inspect California, United States' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('offers Inspect in the page metrics table', () => {
    useStats.mockReturnValue(result({
      data: { columns: ['page', 'visitors', 'pageviews', 'time_on_page', 'exit_rate'], rows: [{ page: '/pricing', visitors: 5, pageviews: 9, time_on_page: 30, exit_rate: 20 }], totals: { visitors: 5 }, meta: { sources_used: ['daily'], cached: false } },
    }))
    const open = vi.fn()
    render(<BreakdownCard title="Pages" tabs={pageTabs} query={baseQuery} onFilter={vi.fn()} inspect={{ can: () => true, open }} />)

    fireEvent.click(screen.getByRole('button', { name: 'Inspect /pricing' }))
    expect(open).toHaveBeenCalledWith({ dimension: 'page', values: ['/pricing'] }, expect.any(HTMLElement))
  })

  it('offers no Inspect where a row has no filter action or the policy blocks it', () => {
    useStats.mockReturnValue(result({ data: data('browser', 'Firefox', 5) }))
    const { unmount } = render(<BreakdownCard title="Browsers" tabs={tabs('browser')} query={baseQuery} onFilter={vi.fn()} inspect={{ can: () => false, open: vi.fn() }} />)
    expect(screen.queryByRole('button', { name: /^Inspect/ })).toBeNull()
    unmount()

    render(<BreakdownCard title="Browsers" tabs={tabs('browser')} query={baseQuery} onFilter={vi.fn()} allowFilter={false} inspect={{ can: () => true, open: vi.fn() }} />)
    expect(screen.queryByRole('button', { name: /^Inspect/ })).toBeNull()
    cleanup()

    useStats.mockReturnValue(result({ data: data('browser_version', '', 5) }))
    render(<BreakdownCard title="Versions" tabs={tabs('browser_version')} query={baseQuery} onFilter={vi.fn()} inspect={{ can: () => true, open: vi.fn() }} />)
    expect(screen.queryByRole('button', { name: /^Inspect/ })).toBeNull()
  })
})
