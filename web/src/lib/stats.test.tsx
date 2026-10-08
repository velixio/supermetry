import type { PropsWithChildren } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}))

vi.mock('./api', () => ({
  api: { GET: get, POST: post },
}))

import { useRealtime, useStats, type StatsQuery } from './stats'

function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const wrapper = ({ children }: PropsWithChildren) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
  return { client, wrapper }
}

const closedQuery: StatsQuery = {
  siteId: 7,
  dateRange: { from: '2026-09-01', to: '2026-09-30' },
  metrics: ['visitors'],
  filters: [],
  timezone: 'UTC',
}

describe('useStats', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-07T20:00:00Z'))
    post.mockResolvedValue({
      data: {
        columns: ['visitors'],
        rows: [],
        totals: { visitors: 4 },
        meta: { sources_used: ['daily'], cached: false },
      },
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('uses a normalized body in the cache key and keeps closed ranges fresh', async () => {
    const { client, wrapper } = setup()
    const { result } = renderHook(() => useStats(closedQuery), { wrapper })

    await vi.waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(post).toHaveBeenCalledWith('/api/private/query', {
      body: {
        site_id: 7,
        date_range: { from: '2026-09-01', to: '2026-09-30' },
        metrics: ['visitors'],
        filters: [],
        timezone: 'UTC',
        include_imported: true,
      },
    })
    const query = client.getQueryCache().getAll()[0]
    expect(query.queryKey).toEqual([
      'stats',
      7,
      {
        site_id: 7,
        date_range: { from: '2026-09-01', to: '2026-09-30' },
        metrics: ['visitors'],
        filters: [],
        timezone: 'UTC',
        include_imported: true,
      },
    ])
    expect(query.observers[0]?.options.staleTime).toBe(300_000)
    expect(query.observers[0]?.options.refetchInterval).toBe(false)
  })

  it('refreshes a live range containing the site-local today', async () => {
    const { client, wrapper } = setup()
    const { result } = renderHook(
      () =>
        useStats({
          ...closedQuery,
          dateRange: { from: '2026-10-08', to: '2026-10-08' },
          timezone: 'Asia/Kathmandu',
          live: true,
        }),
      { wrapper },
    )

    await vi.waitFor(() => expect(result.current.isSuccess).toBe(true))
    const query = client.getQueryCache().getAll()[0]
    expect(query.observers[0]?.options.staleTime).toBe(30_000)
    expect(query.observers[0]?.options.refetchInterval).toBe(30_000)
  })
})

describe('useRealtime', () => {
  it('fetches by site and refreshes every 30 seconds', async () => {
    get.mockResolvedValue({
      data: { active_visitors: 3, top_pages: [] },
    })
    const { client, wrapper } = setup()
    const { result } = renderHook(() => useRealtime(7), { wrapper })

    await vi.waitFor(() => expect(result.current.data?.active_visitors).toBe(3))
    expect(get).toHaveBeenCalledWith('/api/private/realtime', {
      params: { query: { site_id: 7 } },
    })
    expect(
      client.getQueryCache().getAll()[0].observers[0]?.options.refetchInterval,
    ).toBe(30_000)
  })
})
