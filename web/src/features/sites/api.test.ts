import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))

vi.mock('@/lib/api', () => ({ api: { POST: post } }))

import { refetchSiteIcon, SiteIconRefetchError } from './api'

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
        header: { 'X-Panel-Org': '3' },
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
