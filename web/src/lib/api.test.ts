import { describe, expect, it, vi } from 'vitest'

import { createApi } from './api'

describe('api client', () => {
  it('sends the CSRF header on POST and redirects on 401', async () => {
    document.cookie = 'panel_csrf=tok123'
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
    document.cookie = 'panel_csrf=%'
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
