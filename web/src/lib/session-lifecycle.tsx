import { useSyncExternalStore, type ReactNode } from 'react'
import type { QueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { queryClient } from './queryClient'

type Status = 'active' | 'pending' | 'failed' | 'leaving'
const signalKey = 'supermetry-session-change'
const aborted = () => new DOMException('Session changed', 'AbortError')

type Dependencies = {
  client: QueryClient
  fetch: typeof globalThis.fetch
  origin: string
  cookie: () => string
  replace: (url: string) => void
  broadcast: () => void
}

// A separate controller makes lifecycle and late-response behavior testable without navigation.
export function createSessionLifecycle(deps: Dependencies) {
  let status: Status = 'active'
  let epoch = 0
  let pending: Promise<void> | undefined
  const controllers = new Set<AbortController>()
  const listeners = new Set<() => void>()
  const tasks = new Set<() => void>()
  const setStatus = (next: Status) => {
    status = next
    listeners.forEach((listener) => listener())
  }
  const getStatus = () => status
  const clear = () => {
    epoch++
    controllers.forEach((controller) => controller.abort())
    controllers.clear()
    tasks.forEach((cancel) => cancel())
    tasks.clear()
    void deps.client.cancelQueries()
    deps.client.clear()
  }
  const leave = (url = '/login') => {
    setStatus('leaving')
    clear()
    deps.replace(url)
  }
  const guardedFetch: typeof globalThis.fetch = async (input, init) => {
    const url = new URL(input instanceof Request ? input.url : String(input), deps.origin)
    if (url.origin !== deps.origin || !url.pathname.startsWith('/api/private/')) {
      return deps.fetch(input, init)
    }
    if (status !== 'active') throw aborted()
    const started = epoch
    const controller = new AbortController()
    const originalSignal = init?.signal ?? (input instanceof Request ? input.signal : undefined)
    const abort = () => controller.abort()
    if (originalSignal?.aborted) abort()
    originalSignal?.addEventListener('abort', abort, { once: true })
    controllers.add(controller)
    const check = () => { if (started !== epoch || controller.signal.aborted) throw aborted() }
    const release = () => {
      controllers.delete(controller)
      originalSignal?.removeEventListener('abort', abort)
    }
    try {
      const response = await deps.fetch(input, { ...init, signal: controller.signal })
      check()
      if (!response.body) release()
      // Check after body decoding too: headers may arrive before an account changes.
      const guardResponse = (value: Response): Response => new Proxy(value, {
        get(target, property) {
          if (property === 'clone') return () => guardResponse(target.clone())
          const member = Reflect.get(target, property, target)
          if (typeof member !== 'function') return member
          if (['json', 'text', 'blob', 'arrayBuffer', 'formData', 'bytes'].includes(String(property))) {
            return async (...args: unknown[]) => {
              try {
                check()
                const body = await member.apply(target, args)
                check()
                return body
              } finally { release() }
            }
          }
          return member.bind(target)
        },
      })
      return guardResponse(response)
    } catch (error) {
      release()
      throw error
    }
  }
  return {
    fetch: guardedFetch,
    registerPrivateTask(cancel: () => void) {
      if (status !== 'active') throw aborted()
      tasks.add(cancel)
      return () => { tasks.delete(cancel) }
    },
    subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener) } },
    getStatus,
    /** Signs out, then opens `url` (the sign-in page by default). */
    requestLogout(url = '/login'): Promise<void> {
      if (pending) return pending
      if (status === 'leaving') return Promise.resolve()
      setStatus('pending')
      clear()
      pending = (async () => {
        try {
          const raw = deps.cookie().split(';').map((part) => part.trim()).find((part) => part.startsWith('supermetry_csrf='))?.slice('supermetry_csrf='.length)
          let token: string | undefined
          try { token = raw ? decodeURIComponent(raw) : undefined } catch { /* Server rejects missing CSRF. */ }
          const response = await deps.fetch('/api/private/auth/logout', {
            method: 'POST', credentials: 'same-origin', redirect: 'error', signal: AbortSignal.timeout(15_000),
            headers: token ? { 'X-CSRF-Token': token } : {},
          })
          if (response.status !== 204) throw new Error('Logout was not confirmed')
          deps.broadcast()
          leave(url)
        } catch {
          if (getStatus() !== 'leaving') setStatus('failed')
        } finally { pending = undefined }
      })()
      return pending
    },
    returnToApp() { if (status === 'failed') { clear(); setStatus('active') } },
    resetAfterLogin() { clear(); deps.broadcast(); setStatus('active') },
    sessionChanged: leave,
  }
}

let lifecycle: ReturnType<typeof createSessionLifecycle> | undefined
export function installSessionLifecycle() {
  if (lifecycle) return
  const nativeFetch = globalThis.fetch.bind(globalThis)
  let channel: BroadcastChannel | undefined
  try { channel = new BroadcastChannel(signalKey) } catch { /* Storage events remain available. */ }
  lifecycle = createSessionLifecycle({
    client: queryClient, fetch: nativeFetch, origin: window.location.origin,
    cookie: () => document.cookie,
    replace: (url) => window.location.replace(url),
    broadcast: () => {
      try { channel?.postMessage('changed') } catch { /* Storage events remain available. */ }
      try { localStorage.setItem(signalKey, crypto.randomUUID()) } catch { /* Storage may be disabled. */ }
    },
  })
  globalThis.fetch = lifecycle.fetch
  watchSessionChanges(window, () => lifecycle?.sessionChanged(), channel)
}

export function watchSessionChanges(target: Window, changed: () => void, channel?: BroadcastChannel) {
  const storage = (event: StorageEvent) => {
    if (event.key === signalKey && event.newValue) changed()
  }
  // Re-bootstrap restored pages rather than display a frozen old account.
  const pageshow = (event: PageTransitionEvent) => { if (event.persisted) changed() }
  channel?.addEventListener('message', changed)
  target.addEventListener('storage', storage)
  target.addEventListener('pageshow', pageshow)
  return () => {
    channel?.removeEventListener('message', changed)
    target.removeEventListener('storage', storage)
    target.removeEventListener('pageshow', pageshow)
  }
}

export function registerPrivateTask(cancel: () => void) {
  installSessionLifecycle()
  return lifecycle!.registerPrivateTask(cancel)
}

export function requestLogout(url?: string) {
  installSessionLifecycle()
  return lifecycle!.requestLogout(url)
}
export function expireSession(url = '/login') {
  installSessionLifecycle()
  lifecycle!.sessionChanged(url)
}
export function resetAfterLogin() {
  installSessionLifecycle()
  lifecycle!.resetAfterLogin()
}
const noopSubscribe = () => () => undefined
const activeStatus = (): Status => 'active'
export function SessionBoundary({ children, session = lifecycle }: { children: ReactNode; session?: ReturnType<typeof createSessionLifecycle> }) {
  const status = useSyncExternalStore(session?.subscribe ?? noopSubscribe, session?.getStatus ?? activeStatus)
  if (status === 'active') return children
  return (
    <main className="grid min-h-dvh place-items-center bg-background px-5 text-foreground">
      <section className="flex max-w-sm flex-col gap-4" aria-live="polite">
        <h1 className="text-xl font-semibold">{status === 'failed' ? 'Log out could not be confirmed' : 'Logging out…'}</h1>
        {status === 'failed' ? <>
          <p className="text-sm text-muted-foreground">Your session may still be active. Check your connection and retry.</p>
          <Button autoFocus onClick={() => void session?.requestLogout()}>Retry log out</Button>
          <Button variant="outline" onClick={() => session?.returnToApp()}>Return to app</Button>
        </> : <p className="text-sm text-muted-foreground">Closing this session.</p>}
      </section>
    </main>
  )
}
