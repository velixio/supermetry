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
})
