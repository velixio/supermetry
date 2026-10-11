import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { useBreakdown, useStats } = vi.hoisted(() => ({ useBreakdown: vi.fn(), useStats: vi.fn() }))

vi.mock('@/lib/stats', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/stats')>()),
  useBreakdown,
  useStats,
}))

import type { StatsQuery } from '@/lib/stats'
import { WhereCard } from './WhereCard'

const query: StatsQuery = {
  siteId: 7,
  dateRange: { from: '2026-09-28', to: '2026-09-30' },
  metrics: ['visitors'],
  filters: [],
  timezone: 'Asia/Kolkata',
}

const result = (rows: Array<Record<string, unknown>>, totals: Record<string, number> = {}) => ({
  data: { rows, totals, meta: {} }, isPending: false, isError: false, error: null, refetch: vi.fn(), isSuccess: true,
})

describe('WhereCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn().mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }),
    })
    useBreakdown.mockImplementation((request: StatsQuery) => request.dimensions?.[0] === 'country'
      ? result([{ country: 'IN', visitors: 100, pageviews: 200 }, { country: 'US', visitors: 50, pageviews: 75 }])
      : result([{ [request.dimensions?.[0] ?? 'page']: 'Example', visitors: 5 }]))
    useStats.mockReturnValue(result([], { visitors: 100, pageviews: 200, bounce_rate: 25, visit_duration: 90 }))
  })
  afterEach(cleanup)

  it('renders a linked map and ranked list using the dashboard metric', async () => {
    render(<WhereCard metric="visitors" query={query} onFilter={vi.fn()} />)

    expect(screen.getByRole('region', { name: 'Where' })).toBeVisible()
    const mapCountry = await screen.findByRole('button', { name: 'India: 100 visitors, 66.7 percent' })
    expect(mapCountry).toHaveAttribute('aria-describedby', 'where-summary')
    const listCountry = screen.getByRole('button', { name: /India, rank 1, 100 visitors/i })
    fireEvent.mouseEnter(listCountry)
    expect(mapCountry).toHaveAttribute('data-active', 'true')
    fireEvent.mouseEnter(mapCountry)
    expect(listCountry).toHaveAttribute('data-active', 'true')
    expect(screen.getByRole('table', { name: 'Country visitors' })).toBeInTheDocument()
  })

  it('summarizes the selected range and keeps the desktop map prominent', async () => {
    const { container } = render(<WhereCard metric="visitors" query={query} onFilter={vi.fn()} />)

    const summary = screen.getByText(
      'Where from Sep 28, 2026 to Sep 30, 2026. Total 150 visitors. Peak India with 100. Low United States with 50.',
    )
    expect(summary).toHaveClass('sr-only')
    expect(screen.getByText('India leads with 100 visitors.')).toBeVisible()
    const map = await screen.findByRole('group', { name: 'World map' })
    expect(map).toHaveAttribute('aria-describedby', summary.id)
    expect(container.querySelector('[data-map-column]')).toHaveClass('lg:min-h-[260px]')
    expect(map).toHaveClass('lg:min-h-[260px]')
    expect(map.closest('[class*="grid-cols"]')).toHaveClass('xl:grid-cols-[minmax(60%,1fr)_minmax(14rem,40%)]')
    expect(container.querySelector('[data-country-name]')).toHaveClass('whitespace-normal')
  })

  it('selects a country, mounts details lazily, and applies the dashboard filter', async () => {
    const onFilter = vi.fn()
    render(<WhereCard metric="visitors" query={query} onFilter={onFilter} />)

    expect(useStats).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: /India, rank 1/i }))
    expect(await screen.findByRole('heading', { name: /India/ })).toBeVisible()
    expect(useStats).toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Filter the dashboard to this country' }))
    expect(onFilter).toHaveBeenCalledWith({ dimension: 'country', operator: 'is', values: ['IN'] })
    fireEvent.click(screen.getByRole('button', { name: 'Clear selection' }))
    expect(screen.queryByRole('heading', { name: /India/ })).not.toBeInTheDocument()
  })
})
