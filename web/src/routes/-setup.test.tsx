import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { SetupForm, setupRouteDestination } from './setup'

afterEach(cleanup)

function setupFetch(options: { signedIn?: boolean; available?: boolean; status?: number } = {}) {
  return vi.fn<typeof globalThis.fetch>(async (input) => {
    const path = new URL(input instanceof Request ? input.url : String(input), window.location.origin).pathname
    if (path === '/api/private/me') {
      return options.signedIn
        ? new Response('{"user":{"id":1},"memberships":[]}', {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          })
        : new Response(null, { status: 401 })
    }
    if (path === '/api/private/setup/status') {
      return new Response(JSON.stringify({ available: options.available ?? false }), {
        status: options.status ?? 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    throw new Error(`Unexpected request: ${path}`)
  })
}

describe('setup route', () => {
  it('redirects a signed-in user to the app even without a token', async () => {
    const fetch = setupFetch({ signedIn: true })

    await expect(setupRouteDestination(undefined, fetch)).resolves.toBe('/app')
    expect(fetch).toHaveBeenCalledOnce()
  })

  it('marks a signed-out visitor without a token as invalid without probing availability', async () => {
    const fetch = setupFetch()

    await expect(setupRouteDestination(undefined, fetch)).resolves.toBe('invalid')
    expect(fetch).toHaveBeenCalledOnce()
  })

  it('marks setup as invalid when setup is unavailable', async () => {
    const fetch = setupFetch({ available: false })

    await expect(setupRouteDestination('setup-token', fetch)).resolves.toBe('invalid')
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('renders the existing form only when setup is available and a token is present', async () => {
    const fetch = setupFetch({ available: true })

    await expect(setupRouteDestination('setup-token', fetch)).resolves.toBeNull()
    render(<SetupForm token="setup-token" />)
    expect(screen.getByRole('button', { name: /complete setup/i })).toBeVisible()
  })

  it('fails closed when setup availability cannot be checked', async () => {
    const fetch = setupFetch({ status: 503 })

    await expect(setupRouteDestination('setup-token', fetch)).resolves.toBe('invalid')
  })
})
