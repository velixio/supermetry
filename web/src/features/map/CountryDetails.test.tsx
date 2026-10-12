import '@testing-library/jest-dom/vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { useBreakdown, useStats } = vi.hoisted(() => ({ useBreakdown: vi.fn(), useStats: vi.fn() }))

vi.mock('@/lib/stats', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/stats')>()),
  useBreakdown,
  useStats,
}))

import { CountryDetails } from './CountryDetails'
import type { StatsQuery } from '@/lib/stats'

const query: StatsQuery = {
  siteId: 7,
  dateRange: { from: '2026-09-28', to: '2026-09-30' },
  metrics: ['visitors'],
  filters: [],
  timezone: 'Asia/Kolkata',
}

describe('CountryDetails', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn().mockReturnValue({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() }),
    })
    const result = { data: { rows: [], totals: {}, meta: {} }, isPending: false, isError: false, refetch: vi.fn() }
    useStats.mockReturnValue(result)
    useBreakdown.mockReturnValue(result)
  })
  afterEach(cleanup)

  it('lazily scopes all five detail requests to the selected country', async () => {
    render(<CountryDetails country="IN" query={query} onClear={vi.fn()} onFilter={vi.fn()} />)

    expect(useStats).toHaveBeenCalledWith(expect.objectContaining({
      metrics: ['visitors', 'pageviews', 'bounce_rate', 'visit_duration'],
      filters: [{ dimension: 'country', operator: 'is', values: ['IN'] }],
    }))
    expect([...new Set(useBreakdown.mock.calls.map(([request]) => request.dimensions[0]))]).toEqual(['page', 'region', 'city', 'source'])
    for (const [request] of useBreakdown.mock.calls) {
      expect(request.filters).toEqual([{ dimension: 'country', operator: 'is', values: ['IN'] }])
      expect(request.limit).toBe(5)
    }
    expect(await screen.findByRole('dialog')).toHaveClass(
      'bg-popover',
      'pb-[max(1rem,env(safe-area-inset-bottom))]',
      'data-open:animate-none',
    )
  })

  it('hides empty detail sections while keeping populated sections', async () => {
    useStats.mockReturnValue({ data: { rows: [], totals: { visitors: 8 }, meta: {} }, isPending: false, isError: false, refetch: vi.fn() })
    useBreakdown.mockImplementation((request) => ({
      data: { rows: request.dimensions[0] === 'page' ? [{ page: '/pricing', visitors: 8 }] : [] },
      isPending: false,
      isError: false,
      refetch: vi.fn(),
    }))

    render(<CountryDetails country="IN" query={query} onClear={vi.fn()} onFilter={vi.fn()} />)

    expect(await screen.findByRole('heading', { name: 'Top pages' })).toBeVisible()
    expect(screen.queryByRole('heading', { name: 'Regions' })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Cities' })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Sources' })).not.toBeInTheDocument()
  })
})
