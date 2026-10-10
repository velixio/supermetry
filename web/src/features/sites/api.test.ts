import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))
const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))

vi.mock('@/lib/api', () => ({ api: { POST: post } }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { deleteSite, refetchSiteIcon, SiteIconRefetchError } from './api'

describe('refetchSiteIcon', () => {
  beforeEach(() => post.mockReset())

  it('posts to the site icon endpoint with organization context', async () => {
    const site = {
      id: 7,
      public_id: 'docs-public',
      name: 'Docs',
      timezone: 'UTC',
      icon_url: 'https://docs.example/icon.png',
    }
    post.mockResolvedValue({ data: site, response: new Response(null, { status: 200 }) })

    await expect(refetchSiteIcon(7, 3)).resolves.toEqual(site)
    expect(post).toHaveBeenCalledWith('/api/private/sites/{site}/icon/refetch', {
      params: {
        path: { site: 7 },
        header: { 'X-Supermetry-Org': '3' },
      },
    })
  })

  it('preserves the response status for rate-limit handling', async () => {
    post.mockResolvedValue({
      error: { detail: 'too many requests' },
      response: new Response(null, { status: 429 }),
    })

    await expect(refetchSiteIcon(7, 3)).rejects.toEqual(
      expect.objectContaining<Partial<SiteIconRefetchError>>({
        message: 'too many requests',
        status: 429,
      }),
    )
  })
})

describe('deleteSite', () => {
  beforeEach(() => privateRequest.mockReset())

  it('returns the scheduled deletion response', async () => {
    privateRequest.mockResolvedValue({ scheduled: true, purge_at: '2026-10-18T08:00:00Z' })
    await expect(deleteSite(7, 3)).resolves.toEqual({ scheduled: true, purge_at: '2026-10-18T08:00:00Z' })
  })

  it('supports the legacy empty response', async () => {
    privateRequest.mockResolvedValue(undefined)
    await expect(deleteSite(7, 3)).resolves.toEqual({ scheduled: false, purge_at: null })
  })
})
