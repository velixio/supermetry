import { describe, expect, it, vi } from 'vitest'

import { signedInRedirect } from './signed-in'

const me = { user: { id: 1, email: 'owner@example.com', name: 'Owner' }, memberships: [] }

function respond(status: number, body: unknown = {}) {
  return vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
}

describe('signedInRedirect', () => {
  it('returns the target for a signed-in user', async () => {
    const fetchMe = respond(200, me)
    await expect(signedInRedirect('/app/acme', fetchMe)).resolves.toBe('/app/acme')
    expect(fetchMe).toHaveBeenCalledWith('/api/private/me', expect.objectContaining({ credentials: 'same-origin' }))
  })

  it('returns null when the session is missing', async () => {
    await expect(signedInRedirect('/app', respond(401))).resolves.toBeNull()
  })

  it('returns null for an unexpected body', async () => {
    await expect(signedInRedirect('/app', respond(200, { user: null }))).resolves.toBeNull()
  })

  it('returns null when the request fails', async () => {
    const offline = vi.fn(async () => {
      throw new Error('offline')
    })
    await expect(signedInRedirect('/app', offline)).resolves.toBeNull()
  })
})
