import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { deleteRequest, get, post } = vi.hoisted(() => ({ deleteRequest: vi.fn(), get: vi.fn(), post: vi.fn() }))
vi.mock('@/lib/api', () => ({ api: { DELETE: deleteRequest, GET: get, POST: post } }))
import { erasureKeys, profileKeys, useDeleteProfile, useProfile, useProfileErasure, useRetryProfileErasure } from './api'

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
        params: expect.objectContaining({ header: { 'X-Supermetry-Org': '2' } }),
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

  it('keeps the accepted erasure id available for status polling', async () => {
    const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
    deleteRequest.mockResolvedValue({
      response: new Response(null, { status: 202 }),
      data: { erasure_id: 91, status: 'pending' },
    })
    const { result } = renderHook(() => useDeleteProfile(7, 1), {
      wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
    })

    result.current.mutate(123)

    await waitFor(() => expect(result.current.data).toEqual({ erasure_id: 91, status: 'pending' }))
    expect(deleteRequest).toHaveBeenCalledWith('/api/private/profiles/123', expect.objectContaining({
      params: expect.objectContaining({ header: { 'X-Supermetry-Org': '1' } }),
    }))
  })

  it('loads an erasure status and retries the same request', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const erasure = { id: 91, site_id: 7, profile_id: 123, status: 'failed', attempts: 20, last_error: 'storage unavailable', created_at: '', updated_at: '' }
    get.mockResolvedValue({ response: new Response(null, { status: 200 }), data: erasure })
    const retried = { ...erasure, status: 'pending', attempts: 0, last_error: null, updated_at: '2026-10-09T18:30:00Z' }
    post.mockResolvedValue({ response: new Response(null, { status: 200 }), data: retried })
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
    const status = renderHook(() => useProfileErasure(91, 1), { wrapper })
    await waitFor(() => expect(status.result.current.data?.status).toBe('failed'))
    get.mockResolvedValue({ response: new Response(null, { status: 200 }), data: retried })

    const retry = renderHook(() => useRetryProfileErasure(1), { wrapper })
    retry.result.current.mutate(91)
    await waitFor(() => expect(retry.result.current.isSuccess).toBe(true))
    expect(client.getQueryData(erasureKeys.detail(1, 91))).toEqual(retried)

    expect(get).toHaveBeenCalledWith('/api/private/erasures/91', expect.objectContaining({
      params: expect.objectContaining({ header: { 'X-Supermetry-Org': '1' } }),
      signal: expect.any(AbortSignal),
    }))
    expect(post).toHaveBeenCalledWith('/api/private/erasures/91/retry', expect.objectContaining({
      params: expect.objectContaining({ header: { 'X-Supermetry-Org': '1' } }),
    }))
  })
})
