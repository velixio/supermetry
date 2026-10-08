import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'

import { useReplayAvailability } from './api'

const getReplaySettings = vi.fn()
vi.mock('@/lib/replay-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/replay-api')>()),
  getReplaySettings: (...args: unknown[]) => getReplaySettings(...args),
}))

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>
}

describe('useReplayAvailability', () => {
  it.each([true, false])('follows the environment switch (enabled=%s)', async (enabled) => {
    getReplaySettings.mockResolvedValueOnce({ enabled })
    const { result } = renderHook(() => useReplayAvailability(1), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.enabled).toBe(enabled)
  })
})
