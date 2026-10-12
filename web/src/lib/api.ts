import createClient, { type Middleware } from 'openapi-fetch'
import { queryClient } from './queryClient'

import { expireSession } from './session-lifecycle'

import type { paths } from './api.gen'

type PrivatePaths = {
  [Path in keyof paths as Path extends `/api/public/v1${infer Rest}`
    ? `/api/private${Rest}`
    : Path extends `/api/v1${infer Rest}`
      ? `/api/private${Rest}`
    : Path extends `/api/private${string}`
      ? Path
      : never]: paths[Path]
} & {
  '/api/private/auth/config': {
    get: {
      responses: {
        200: {
          content: {
            'application/json': {
              signup: 'open' | 'invite'
              setup_required: boolean
            }
          }
        }
      }
    }
  }
  '/api/private/auth/signup': {
    post: {
      requestBody: {
        content: {
          'application/json': {
            email: string
            name: string
            password: string
            org_name: string
          }
        }
      }
      responses: {
        201: {
          content: {
            'application/json': { authenticated: boolean }
          }
        }
      }
    }
  }
  '/api/private/me': {
    get: {
      responses: {
        200: {
          content: {
            'application/json': unknown
          }
        }
      }
    }
  }
  // Private-only endpoint, typed by hand like /auth/* above (not part of the public OpenAPI spec).
  '/api/private/sites/{site}/icon/refetch': {
    post: {
      parameters: {
        path: { site: number }
        header: { 'X-Supermetry-Org': string }
      }
      responses: {
        200: {
          content: {
            'application/json': {
              id: number
              public_id: string
              name: string
              timezone: string
              retention_days?: number
              allowed_hosts?: string[]
              profiles_enabled?: boolean
              install_status?: 'waiting' | 'receiving' | 'quiet'
              last_seen_at?: string | null
              icon_url: string
            }
          }
        }
        403: { content: { 'application/problem+json': { detail?: string } } }
        429: { content: { 'application/problem+json': { detail?: string } } }
      }
    }
  }
}

type ApiOptions = {
  fetch?: typeof globalThis.fetch
  onUnauthorized?: (loginURL: string) => void
}

const csrfCookie = 'supermetry_csrf'

function readCookie(name: string): string | undefined {
  const prefix = `${encodeURIComponent(name)}=`
  const cookie = document.cookie
    .split(';')
    .map((part) => part.trim())
    .find((part) => part.startsWith(prefix))
  if (!cookie) return undefined
  try {
    return decodeURIComponent(cookie.slice(prefix.length))
  } catch {
    return undefined
  }
}

function loginURL(): string {
  const next = `${window.location.pathname}${window.location.search}${window.location.hash}`
  return `/login?next=${encodeURIComponent(next)}`
}

export function createApi(options: ApiOptions = {}) {
  const client = createClient<PrivatePaths>({
    baseUrl: window.location.origin,
    fetch: options.fetch ?? ((...args) => globalThis.fetch(...args)),
  })
  const onUnauthorized =
    options.onUnauthorized ?? ((url: string) => expireSession(url))
  const middleware: Middleware = {
    async onRequest({ request }) {
      if (!request.headers.has('X-Supermetry-Org')) {
        const me = queryClient.getQueryData<{ memberships: { org: { id: number; slug: string } }[] }>(['me'])
        let slug = ''
        try { slug = decodeURIComponent(window.location.pathname.split('/')[2] ?? '') } catch { /* Invalid paths have no tenant context. */ }
        const membership = me?.memberships.find((item) => item.org.slug === slug)
        if (membership) request.headers.set('X-Supermetry-Org', String(membership.org.id))
      }
      if (request.method !== 'GET') {
        const token = readCookie(csrfCookie)
        if (token) {
          request.headers.set('X-CSRF-Token', token)
        }
      }
      return request
    },
    async onResponse({ response }) {
      if (response.status === 401) {
        onUnauthorized(loginURL())
      }
      return response
    },
  }
  client.use(middleware)
  return client
}

export const api = createApi()
