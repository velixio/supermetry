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
    get.mockResolvedValue({
      data: { active_visitors: 4, top_pages: [] },
      response: { status: 200 },
    })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  it('polls realtime data every 10 seconds', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    )
    const { result } = renderHook(() => useRealtimePage(7), { wrapper })

    await vi.waitFor(() => expect(result.current.data?.active_visitors).toBe(4))
    expect(get).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(10_000)
    await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  })
})
