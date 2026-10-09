import type { PropsWithChildren } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))

vi.mock('@/lib/api', () => ({ api: { POST: post } }))

import { useFunnelResult } from './api'

describe('useFunnelResult', () => {
  it('includes the visitor counting basis and window in the request and cache identity', async () => {
    post.mockResolvedValue({
      data: { count_by: 'visitors', steps: [{ step: 1, count: 3, conversion: 100 }] },
      response: { status: 200 },
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    )
    const steps = [
      { type: 'page' as const, match: 'exact' as const, value: '/' },
      { type: 'event' as const, match: 'exact' as const, value: 'signup' },
    ]
    const range = { from: '2026-10-01', to: '2026-10-07' }
    const { result } = renderHook(() => useFunnelResult(7, range, steps, 'visitors', '24h'), { wrapper })

    await vi.waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(post).toHaveBeenCalledWith('/api/private/funnels/query', {
      body: { site_id: 7, date_range: range, steps, count_by: 'visitors', window: '24h' },
    })
    expect(client.getQueryCache().getAll()[0].queryKey).toEqual([
      'funnels', 7, 'result', steps, '2026-10-01', '2026-10-07', 'visitors', '24h',
    ])
  })
})
