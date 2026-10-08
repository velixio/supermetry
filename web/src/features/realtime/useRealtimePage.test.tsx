import type { PropsWithChildren } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock('@/lib/api', () => ({ api: { GET: get } }))

import { useRealtimePage } from './useRealtimePage'

describe('useRealtimePage', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    get.mockResolvedValue({
      data: { active_visitors: 4, visitors_last_30m: 11, top_pages: [] },
      response: { status: 200 },
    })
  })

  afterEach(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  it('polls realtime data every 5 seconds while the document is visible', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    )
    const { result } = renderHook(() => useRealtimePage(7), { wrapper })

    await vi.waitFor(() => expect(result.current.data?.active_visitors).toBe(4))
    expect(client.getQueryData(['realtime-summary', 7])).toEqual({
      active_visitors: 4,
      visitors_last_30m: 11,
    })
    expect(get).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(5_000)
    await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  })

  it('pauses polling while the document is hidden', async () => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: true })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    )
    const { result } = renderHook(() => useRealtimePage(7), { wrapper })

    await vi.waitFor(() => expect(result.current.data?.active_visitors).toBe(4))
    await vi.advanceTimersByTimeAsync(15_000)

    expect(get).toHaveBeenCalledTimes(1)
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
  })
})
