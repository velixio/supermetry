import { describe, expect, it, vi } from 'vitest'

import { createShareApi } from './api'

describe('shared dashboard API', () => {
  it('turns an unauthenticated share response into a password prompt problem', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          type: 'https://supermetry.com/problems/unauthorized',
          title: 'Unauthorized',
          status: 401,
          detail: 'valid authentication is required',
        }),
        {
          status: 401,
          headers: { 'Content-Type': 'application/problem+json' },
        },
      ),
    )
    const shareApi = createShareApi(fetchMock)

    await expect(shareApi.loadSummary('protected')).rejects.toMatchObject({
      type: 'share-password-required',
      status: 401,
    })
  })

  it('loads only an enabled section through the server-scoped query route', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ totals: {}, columns: ['page'], rows: [{ page: '/docs' }] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const shareApi = createShareApi(fetchMock)

    await expect(shareApi.loadSection('shared', 'pages')).resolves.toMatchObject({ rows: [{ page: '/docs' }] })
    const request = fetchMock.mock.calls[0]?.[0] as Request
    expect(request.url).toBe('http://localhost:3000/api/private/share/shared/query')
    expect(request.method).toBe('POST')
    await expect(request.clone().text()).resolves.toBe(JSON.stringify({ section: 'pages' }))
  })
})
