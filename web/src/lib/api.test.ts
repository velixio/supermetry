import { afterEach, describe, expect, it, vi } from 'vitest'
import { queryClient } from './queryClient'

import { createApi } from './api'

afterEach(() => { queryClient.clear(); window.history.replaceState({}, '', '/') })

describe('api client', () => {
  it('selects only the organization named by the current route and preserves explicit context', async () => {
    queryClient.setQueryData(['me'], { memberships: [{ org: { id: 1, slug: 'first' } }, { org: { id: 2, slug: 'second' } }] })
    const requests: Request[] = []
    const api = createApi({ fetch: vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => { requests.push(input instanceof Request ? input : new Request(input, init)); return new Response('{}') }) })
    window.history.replaceState({}, '', '/app/second/site')
    await api.GET('/api/private/me', {})
    expect(requests[0].headers.get('X-Supermetry-Org')).toBe('2')
    await api.GET('/api/private/me', { headers: { 'X-Supermetry-Org': '1' } })
    expect(requests[1].headers.get('X-Supermetry-Org')).toBe('1')
    window.history.replaceState({}, '', '/app/account/profile')
    await api.GET('/api/private/me', {})
    expect(requests[2].headers.has('X-Supermetry-Org')).toBe(false)
    queryClient.clear()
    window.history.replaceState({}, '', '/app/second/site')
    await api.GET('/api/private/me', {})
    expect(requests[3].headers.has('X-Supermetry-Org')).toBe(false)
  })
  it('sends the CSRF header on POST and redirects on 401', async () => {
    document.cookie = 'supermetry_csrf=tok123'
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response('{}', { status: 200 }))
      .mockResolvedValueOnce(
        new Response('{"status":401}', {
          status: 401,
          headers: { 'Content-Type': 'application/problem+json' },
        }),
      )
    const nav = vi.fn()
    const api = createApi({ fetch: fetchMock, onUnauthorized: nav })

    await api.POST('/api/private/query', {
      body: { site_id: 1, metrics: ['visitors'], period: '7d' } as never,
    })
    expect(fetchMock.mock.calls[0][0].headers.get('X-CSRF-Token')).toBe('tok123')

    await api.GET('/api/private/me', {})
    expect(nav).toHaveBeenCalledWith(expect.stringContaining('/login?next='))
  })

  it('ignores a malformed CSRF cookie value', async () => {
    document.cookie = 'supermetry_csrf=%'
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }))
    const api = createApi({ fetch: fetchMock })

    await expect(
      api.POST('/api/private/query', {
        body: { site_id: 1, metrics: ['visitors'], period: '7d' } as never,
      }),
    ).resolves.toBeDefined()
    expect(fetchMock.mock.calls[0][0].headers.has('X-CSRF-Token')).toBe(false)
  })
})
