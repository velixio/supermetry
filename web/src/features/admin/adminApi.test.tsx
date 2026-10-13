import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { adminKeys, useApplyUpdate, useUpdateStatus } from './adminApi'

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

  it('polls while an update job is active', async () => {
    privateRequest.mockResolvedValue({
      current_version: '1.2.0', latest_version: '1.3.0', update_available: true,
      release_url: null, published_at: null, checked_at: '2026-10-14T09:00:00Z', check_error: null,
      update_supported: true,
      job: {
        id: 7, from_version: 'v1.2.0', target_version: 'v1.3.0', deployment_mode: 'binary',
        status: 'downloading', created_at: '2026-10-14T09:01:00Z', updated_at: '2026-10-14T09:01:10Z',
      },
    })
    const { client, result } = setup(true)

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    const query = client.getQueryCache().find({ queryKey: adminKeys.update })
    const interval = (query?.options as { refetchInterval?: (query: { state: { data?: unknown } }) => number | false }).refetchInterval
    expect(typeof interval).toBe('function')
    expect(interval?.({ state: { data: query?.state.data } })).toBe(2_000)
  })
})

describe('useApplyUpdate', () => {
  it('posts the sensitive request and seeds the status cache with the accepted job', async () => {
    const accepted = {
      id: 8, from_version: 'v1.2.0', target_version: 'v1.3.0', deployment_mode: 'docker' as const,
      status: 'requested' as const, created_at: '2026-10-14T10:00:00Z', updated_at: '2026-10-14T10:00:00Z',
    }
    const current = {
      current_version: '1.2.0', latest_version: '1.3.0', update_available: true,
      release_url: null, published_at: null, checked_at: '2026-10-14T09:00:00Z', check_error: null,
      update_supported: true, deployment_mode: 'docker' as const, job: null,
    }
    privateRequest.mockResolvedValue(accepted)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    client.setQueryData(adminKeys.update, current)
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    )
    const { result } = renderHook(() => useApplyUpdate(), { wrapper })
    const body = { target_version: 'v1.3.0', idempotency_key: 'update-123', password: 'secret', totp_code: '123456' }

    result.current.mutate(body)
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(privateRequest).toHaveBeenCalledWith('POST', '/api/private/admin/update/apply', { body })
    expect(client.getQueryData(adminKeys.update)).toEqual({ ...current, job: accepted })
  })
})
