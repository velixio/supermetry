import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/lib/api', () => ({ api: { GET: get } }))
import { profileKeys, useProfile } from './api'

const foreignProfile = {
  id: 123,
  site_id: 7,
  external_id: 'CACHED_ORG_A_SECRET',
  traits: { email: 'a@example.test' },
  timeline: [],
}
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('profile query authorization scope', () => {
  it('never reuses an organization A cached profile in organization B or another site', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: 30_000 } },
    })
    client.setQueryData(profileKeys.detail(1, 7, 123), foreignProfile)
    get.mockResolvedValue({
      response: new Response(null, { status: 403 }),
      error: { detail: 'Forbidden' },
    })
    const { result } = renderHook(() => useProfile(123, 2, 20), {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    })
    expect(result.current.data).toBeUndefined()
    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(result.current.data).toBeUndefined()
    expect(get).toHaveBeenCalledWith(
      '/api/private/profiles/123',
      expect.objectContaining({
        params: expect.objectContaining({ header: { 'X-Panel-Org': '2' } }),
        signal: expect.any(AbortSignal),
      }),
    )
    expect(profileKeys.detail(1, 7, 123)).not.toEqual(
      profileKeys.detail(1, 20, 123),
    )
  })

  it('rejects a successful response whose site differs from the selected site', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    get.mockResolvedValue({
      response: new Response(null, { status: 200 }),
      data: foreignProfile,
    })
    const { result } = renderHook(() => useProfile(123, 1, 20), {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    })
    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(result.current.error?.message).toContain('selected site')
    expect(result.current.data).toBeUndefined()
    expect(client.getQueryData(profileKeys.detail(1, 20, 123))).toBeUndefined()
  })
})
