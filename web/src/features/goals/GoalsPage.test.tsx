import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post, useStats } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  useStats: vi.fn(),
}))

vi.mock('@/lib/api', () => ({ api: { GET: get, POST: post } }))
vi.mock('@/lib/stats', () => ({ useStats }))

import { GoalsPage } from './GoalsPage'

describe('GoalsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get
      .mockResolvedValueOnce({ data: [], response: { status: 200 } })
      .mockResolvedValue({
        data: [
          {
            id: 11,
            site_id: 7,
            kind: 'page',
            match: 'exact',
            value: '/thanks',
            name: 'Signup complete',
          },
        ],
        response: { status: 200 },
      })
    post.mockResolvedValue({
      data: {
        id: 11,
        site_id: 7,
        kind: 'page',
        match: 'exact',
        value: '/thanks',
        name: 'Signup complete',
      },
      response: { status: 201 },
    })
    useStats.mockReturnValue({
      data: { rows: [] },
      isPending: false,
      isError: false,
    })
  })

  afterEach(() => cleanup())

  it('shows a created goal after invalidating and refetching the goals list', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <GoalsPage
          siteId={7}
          dateRange={{ from: '2026-10-01', to: '2026-10-07' }}
        />
      </QueryClientProvider>,
    )

    await waitFor(() => expect(get).toHaveBeenCalledTimes(1))
    fireEvent.change(screen.getByLabelText('Goal name'), {
      target: { value: 'Signup complete' },
    })
    fireEvent.change(screen.getByLabelText('Goal value'), {
      target: { value: '/thanks' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Create goal' }))

    expect(await screen.findByText('Signup complete')).toBeVisible()
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('queries and displays conversions for each goal definition', async () => {
    get.mockReset()
    useStats.mockReset()
    get.mockResolvedValue({
      data: [{ id: 4, site_id: 7, kind: 'page', match: 'exact', value: '/thanks', name: 'Signup complete' }],
      response: { status: 200 },
    })
    useStats.mockImplementation((query: { filters: { dimension: string; values: string[] }[] }) => ({
      data: { totals: query.filters.length > 0 ? { visitors: 8 } : { visitors: 80 } },
      isPending: false,
      isError: false,
    }))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    render(<QueryClientProvider client={client}><GoalsPage siteId={7} dateRange={{ from: '2026-10-01', to: '2026-10-07' }} /></QueryClientProvider>)

    expect(await screen.findByText('Signup complete')).toBeVisible()
    expect(screen.getByText('8')).toBeVisible()
    expect(screen.getByText('10%')).toBeVisible()
    expect(useStats).toHaveBeenCalledWith(expect.objectContaining({
      filters: [{ dimension: 'page', operator: 'is', values: ['/thanks'] }],
    }))
  })

  it('shows currency-formatted revenue only for revenue goals', async () => {
    get.mockReset()
    useStats.mockReset()
    get.mockResolvedValue({
      data: [
        { id: 4, site_id: 7, kind: 'event', match: 'exact', value: 'purchase', name: 'Purchase', currency: 'USD' },
        { id: 5, site_id: 7, kind: 'page', match: 'exact', value: '/thanks', name: 'Signup complete' },
      ],
      response: { status: 200 },
    })
    useStats.mockImplementation((query: { metrics: string[]; filters: { dimension: string; values: string[] }[] }) => ({
      data: {
        totals: query.metrics.includes('total_revenue')
          ? { visitors: 2, total_revenue: 123.45, average_revenue: 61.725 }
          : query.filters.length > 0 ? { visitors: 8 } : { visitors: 80 },
      },
      isPending: false,
      isError: false,
    }))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    render(<QueryClientProvider client={client}><GoalsPage siteId={7} dateRange={{ from: '2026-10-01', to: '2026-10-07' }} /></QueryClientProvider>)

    expect(await screen.findByText('Purchase')).toBeVisible()
    expect(screen.getByText('$123.45')).toBeVisible()
    expect(screen.getByText('$61.73')).toBeVisible()
    expect(screen.getAllByText('—')).toHaveLength(2)
    expect(useStats).toHaveBeenCalledWith(expect.objectContaining({
      metrics: ['total_revenue', 'average_revenue'],
      filters: [
        { dimension: 'event', operator: 'is', values: ['purchase'] },
        { dimension: 'revenue_currency', operator: 'is', values: ['USD'] },
      ],
    }))
  })
})
