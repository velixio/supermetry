import type { AuthConfig } from '@/features/auth/api'

let inflight: { fetcher: typeof fetch; request: Promise<Partial<AuthConfig> | null> } | null = null

/**
 * Reads the public instance settings without React Query, for components that also render
 * on the auth screens. Callers that mount together share one request.
 */
export function loadPublicConfig(): Promise<Partial<AuthConfig> | null> {
  const fetcher = globalThis.fetch
  if (inflight && inflight.fetcher === fetcher) return inflight.request
  const request = Promise.resolve()
    .then(() => fetcher('/api/private/auth/config', { credentials: 'same-origin', headers: { Accept: 'application/json' } }))
    .then(async (response) => (response.ok ? ((await response.json()) as Partial<AuthConfig>) : null))
    .catch(() => null)
  const entry = { fetcher, request }
  inflight = entry
  setTimeout(() => { if (inflight === entry) inflight = null }, 0)
  return request
}
