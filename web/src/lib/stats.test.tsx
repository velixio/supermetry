import type { PropsWithChildren } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook } from '@testing-library/react'
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
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
    expect(post).toHaveBeenCalledTimes(2)
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
    ).toEqual(expect.any(Function))
  })
})

describe('authorization failures', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-07T20:00:00Z'))
  })
  afterEach(() => vi.useRealTimers())

  it.each([401, 403])('preserves %s without retries or live polling', async (status) => {
    const problem = { type: 'about:blank', title: 'Restricted', status }
    post.mockResolvedValue({ error: problem })
    get.mockResolvedValue({ error: problem })
    const { client, wrapper } = setup()
    client.setDefaultOptions({ queries: { retry: 1, retryDelay: 10 } })
    const { result, unmount } = renderHook(() => ({
      stats: useStats({ ...closedQuery, dateRange: { from: '2026-10-07', to: '2026-10-07' }, live: true }),
      realtime: useRealtime(7),
    }), { wrapper })
    await vi.waitFor(() => {
      expect(result.current.stats.error).toBe(problem)
      expect(result.current.realtime.error).toBe(problem)
    })
    await act(async () => { await vi.advanceTimersByTimeAsync(90_000) })
    expect(post).toHaveBeenCalledTimes(1)
    expect(get).toHaveBeenCalledTimes(1)
    expect(result.current.stats.data).toBeUndefined()
    expect(result.current.realtime.data).toBeUndefined()
    unmount()
    client.clear()
  })

  it('honors a query-specific disabled retry for transient failures', async () => {
    const failure = { type: 'about:blank', title: 'Unavailable', status: 503 }
    post.mockResolvedValue({ error: failure })
    const { client, wrapper } = setup()
    client.setDefaultOptions({ queries: { retry: 3, retryDelay: 10 } })
    client.setQueryDefaults(['stats'], { retry: false })
    const { result, unmount } = renderHook(() => useStats(closedQuery), { wrapper })
    await vi.waitFor(() => expect(result.current.error).toBe(failure))
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(post).toHaveBeenCalledTimes(1)
    unmount()
    client.clear()
  })

  it('keeps configured transient retries and successful polling', async () => {
    const failure = { type: 'about:blank', title: 'Unavailable', status: 503 }
    get.mockResolvedValueOnce({ error: failure }).mockResolvedValue({ data: { active_visitors: 3, top_pages: [] } })
    const { client, wrapper } = setup()
    client.setDefaultOptions({ queries: { retry: 1, retryDelay: 10 } })
    const { result, unmount } = renderHook(() => useRealtime(7), { wrapper })
    await vi.waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(get).toHaveBeenCalledTimes(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
    expect(get).toHaveBeenCalledTimes(3)
    unmount()
    client.clear()
  })
})

describe('tracking mode cache isolation', () => {
  it('does not reuse identified stats or realtime when mode becomes anonymous', async () => {
    const identified = { columns: ['visitors'], rows: [], totals: { visitors: 99 }, meta: { sources_used: ['daily'], cached: false } }
    const restricted = { type: 'about:blank', title: 'Restricted', status: 403 }
    post.mockReset().mockResolvedValueOnce({ data: identified }).mockResolvedValue({ error: restricted })
    get.mockReset().mockResolvedValueOnce({ data: { active_visitors: 99, top_pages: [] } }).mockResolvedValue({ error: restricted })
    const { client, wrapper } = setup()
    const { result, rerender, unmount } = renderHook(({ mode }: { mode: StatsQuery['trackingMode'] }) => ({
      stats: useStats({ ...closedQuery, trackingMode: mode }),
      realtime: useRealtime(7, mode),
    }), { wrapper, initialProps: { mode: 'identified' } })
    await vi.waitFor(() => expect(result.current.stats.data).toBe(identified))
    await vi.waitFor(() => expect(result.current.realtime.data?.active_visitors).toBe(99))
    rerender({ mode: 'anonymous' })
    expect(result.current.stats.data).toBeUndefined()
    expect(result.current.realtime.data).toBeUndefined()
    await vi.waitFor(() => expect(result.current.stats.error).toBe(restricted))
    await vi.waitFor(() => expect(result.current.realtime.error).toBe(restricted))
    expect(post.mock.calls[0]).toEqual(post.mock.calls[1])
    expect(get.mock.calls[0]).toEqual(get.mock.calls[1])
    expect(client.getQueryCache().getAll()).toHaveLength(4)
    unmount()
    client.clear()
  })
})
