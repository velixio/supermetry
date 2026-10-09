import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { createSessionLifecycle } from './session-lifecycle'

function setup(fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(null, { status: 204 }))) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  client.setQueryData(['me'], { id: 'previous' })
  const replace = vi.fn()
  const broadcast = vi.fn()
  const session = createSessionLifecycle({ client, fetch, origin: 'http://localhost', cookie: () => 'panel_csrf=csrf%20value', replace, broadcast })
  return { session, client, fetch, replace, broadcast }
}

describe('session lifecycle', () => {
  it('deduplicates logout, sends CSRF, clears queries and mutations, and replaces login', async () => {
    let resolve!: (value: Response) => void
    const fetch = vi.fn<typeof globalThis.fetch>(() => new Promise((done) => { resolve = done }))
    const { session, client, replace, broadcast } = setup(fetch)
    client.getMutationCache().build(client, { mutationKey: ['private-edit'] })
    const first = session.requestLogout()
    expect(session.requestLogout()).toBe(first)
    expect(session.getStatus()).toBe('pending')
    expect(client.getQueryCache().getAll()).toHaveLength(0)
    expect(client.getMutationCache().getAll()).toHaveLength(0)
    expect(fetch).toHaveBeenCalledExactlyOnceWith('/api/private/auth/logout', { method: 'POST', credentials: 'same-origin', redirect: 'error', signal: expect.any(AbortSignal), headers: { 'X-CSRF-Token': 'csrf value' } })
    resolve(new Response(null, { status: 204 }))
    await first
    expect(broadcast).toHaveBeenCalledOnce()
    expect(replace).toHaveBeenCalledExactlyOnceWith('/login')
    expect(session.getStatus()).toBe('leaving')
  })
  it.each([204])('accepts verified logout status %s', async (status) => {
    const { session, replace } = setup(vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(null, { status })))
    await session.requestLogout()
    expect(replace).toHaveBeenCalledWith('/login')
  })
  it.each([200, 401, 403, 500, 'network'])('keeps failure %s honest and supports retry', async (failure) => {
    const fetch = vi.fn<typeof globalThis.fetch>()
    if (failure === 'network') fetch.mockRejectedValueOnce(new Error('offline'))
    else fetch.mockResolvedValueOnce(new Response(null, { status: Number(failure) }))
    fetch.mockResolvedValueOnce(new Response(null, { status: 204 }))
    const { session, replace, broadcast } = setup(fetch)
    await session.requestLogout()
    expect(session.getStatus()).toBe('failed')
    expect(replace).not.toHaveBeenCalled()
    expect(broadcast).not.toHaveBeenCalled()
    await expect(session.fetch('/api/private/me')).rejects.toMatchObject({ name: 'AbortError' })
    await session.requestLogout()
    expect(replace).toHaveBeenCalledWith('/login')
  })
  it('resumes with empty cache after a failure', async () => {
    const { session, client } = setup(vi.fn<typeof globalThis.fetch>().mockRejectedValue(new Error('offline')))
    await session.requestLogout()
    session.returnToApp()
    expect(session.getStatus()).toBe('active')
    expect(client.getQueryData(['me'])).toBeUndefined()
  })
  it('aborts requests and rejects late results even if the transport ignores abort', async () => {
    let resolve!: (value: Response) => void
    const fetch = vi.fn<typeof globalThis.fetch>(() => new Promise((done) => { resolve = done }))
    const { session } = setup(fetch)
    const result = session.fetch('/api/private/me')
    const signal = fetch.mock.calls[0][1]?.signal
    session.sessionChanged()
    expect(signal?.aborted).toBe(true)
    resolve(new Response('{}'))
    await expect(result).rejects.toMatchObject({ name: 'AbortError' })
  })
  it('rejects response bodies delivered after an account switch', async () => {
    const { session, client, broadcast } = setup(vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response('{"id":"old"}')))
    const response = await session.fetch('/api/private/me')
    session.resetAfterLogin()
    await expect(response.json()).rejects.toMatchObject({ name: 'AbortError' })
    expect(client.getQueryCache().getAll()).toHaveLength(0)
    expect(broadcast).toHaveBeenCalledOnce()
  })
  it('leaves external and public requests alone', async () => {
    const { session, fetch } = setup()
    session.sessionChanged()
    await session.fetch('https://external.test/api/private/me')
    await session.fetch('/api/public/stats')
    expect(fetch).toHaveBeenCalledTimes(2)
  })
  it('respects caller cancellation', async () => {
    const { session } = setup()
    const controller = new AbortController()
    controller.abort()
    await expect(session.fetch('/api/private/me', { signal: controller.signal })).rejects.toMatchObject({ name: 'AbortError' })
  })
})
