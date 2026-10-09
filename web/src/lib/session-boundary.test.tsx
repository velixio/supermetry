import '@testing-library/jest-dom/vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { QueryClient } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'
import { createSessionLifecycle, SessionBoundary, watchSessionChanges } from './session-lifecycle'

afterEach(cleanup)
it('unmounts private content, shows honest failure and supports retry', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>()
    .mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
  const session = createSessionLifecycle({ client: new QueryClient(), fetch, origin: window.location.origin, cookie: () => '', replace: vi.fn(), broadcast: vi.fn() })
  render(<SessionBoundary session={session}><div>Private account data</div></SessionBoundary>)
  expect(screen.getByText('Private account data')).toBeInTheDocument()
  await act(() => session.requestLogout())
  expect(screen.queryByText('Private account data')).not.toBeInTheDocument()
  expect(screen.getByRole('heading', { name: 'Log out could not be confirmed' })).toBeInTheDocument()
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Retry log out' })) })
  expect(fetch).toHaveBeenCalledTimes(2)
  expect(screen.getByRole('heading', { name: 'Logging out…' })).toBeInTheDocument()
})
it('returns to a fresh app after failed logout', async () => {
  const session = createSessionLifecycle({ client: new QueryClient(), fetch: vi.fn().mockRejectedValue(new Error('offline')), origin: window.location.origin, cookie: () => '', replace: vi.fn(), broadcast: vi.fn() })
  render(<SessionBoundary session={session}><div>Private account data</div></SessionBoundary>)
  await act(() => session.requestLogout())
  fireEvent.click(screen.getByRole('button', { name: 'Return to app' }))
  expect(screen.getByText('Private account data')).toBeInTheDocument()
})
it('watches cross-tab and bfcache events without reacting to theme preferences', () => {
  const changed = vi.fn()
  const channel = new EventTarget() as BroadcastChannel
  const stop = watchSessionChanges(window, changed, channel)
  window.dispatchEvent(new StorageEvent('storage', { key: 'panel-theme', newValue: 'dark' }))
  window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: false }))
  expect(changed).not.toHaveBeenCalled()
  window.dispatchEvent(new StorageEvent('storage', { key: 'panel-session-change', newValue: 'unique-id' }))
  channel.dispatchEvent(new MessageEvent('message', { data: 'changed' }))
  window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }))
  expect(changed).toHaveBeenCalledTimes(3)
  stop()
  channel.dispatchEvent(new MessageEvent('message', { data: 'changed' }))
  expect(changed).toHaveBeenCalledTimes(3)
})
