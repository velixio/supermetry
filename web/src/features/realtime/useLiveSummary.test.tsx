import type { PropsWithChildren } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock('@/lib/api', () => ({ api: { GET: get } }))

import { useLiveSummary } from './useLiveSummary'

function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retryDelay: 0 } },
  })
  const wrapper = ({ children }: PropsWithChildren) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
  return { client, wrapper }
}

describe('useLiveSummary', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    get.mockResolvedValue({
      data: { active_visitors: 12, visitors_last_30m: 48 },
      response: { status: 200 },
    })
  })

  afterEach(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  it('uses the site-scoped cache key and polls every 30 seconds while visible', async () => {
    const { client, wrapper } = setup()
    const { result } = renderHook(() => useLiveSummary(7), { wrapper })

    await vi.waitFor(() => expect(result.current.data?.active_visitors).toBe(12))
    expect(client.getQueryData(['realtime-summary', 7])).toEqual({
      active_visitors: 12,
      visitors_last_30m: 48,
    })

    await vi.advanceTimersByTimeAsync(30_000)
    await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  })

  it('pauses while hidden and resumes after visibility changes', async () => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: true })
    const { wrapper } = setup()
    const { result } = renderHook(() => useLiveSummary(7), { wrapper })

    await vi.waitFor(() => expect(result.current.data?.active_visitors).toBe(12))
    await vi.advanceTimersByTimeAsync(60_000)
    expect(get).toHaveBeenCalledTimes(1)

    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    await act(async () => document.dispatchEvent(new Event('visibilitychange')))
    await vi.advanceTimersByTimeAsync(30_000)
    await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  })

  it('does not retry forbidden responses', async () => {
    get.mockResolvedValue({
      error: { type: 'about:blank', title: 'Forbidden', status: 403 },
      response: { status: 403 },
    })
    const { wrapper } = setup()
    const { result } = renderHook(() => useLiveSummary(7), { wrapper })

    await vi.waitFor(() => expect(result.current.isError).toBe(true))
    await vi.advanceTimersByTimeAsync(1_000)
    expect(get).toHaveBeenCalledTimes(1)
  })
})
