import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { adminKeys, useUpdateStatus } from './adminApi'

function setup(enabled: boolean) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
  const view = renderHook(() => useUpdateStatus(enabled), { wrapper })
  return { client, ...view }
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('useUpdateStatus', () => {
  it('does not make the admin request when disabled', async () => {
    setup(false)
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(privateRequest).not.toHaveBeenCalled()
  })

  it('uses the admin update key and low-frequency, no-retry policy', async () => {
    privateRequest.mockResolvedValue({
      current_version: '1.2.0', latest_version: '1.3.0', update_available: true,
      release_url: 'https://github.com/velixio/supermetry/releases/tag/v1.3.0',
      published_at: '2026-10-14T08:00:00Z', checked_at: '2026-10-14T09:00:00Z', check_error: null,
    })
    const { client, result } = setup(true)

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(privateRequest).toHaveBeenCalledWith('GET', '/api/private/admin/update')
    const query = client.getQueryCache().find({ queryKey: adminKeys.update })
    const options = query?.options as { staleTime?: number; retry?: unknown; refetchOnWindowFocus?: unknown }
    expect(options.staleTime).toBe(6 * 60 * 60 * 1000)
    expect(options.retry).toBe(false)
    expect(options.refetchOnWindowFocus).toBe(false)
  })
})
