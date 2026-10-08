import createClient, { type Middleware } from 'openapi-fetch'

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
        header: { 'X-Panel-Org': string }
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
              last_event_received_at?: string
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

const csrfCookie = 'panel_csrf'

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
    fetch: options.fetch,
  })
  const onUnauthorized =
    options.onUnauthorized ?? ((url: string) => window.location.assign(url))
  const middleware: Middleware = {
    async onRequest({ request }) {
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
